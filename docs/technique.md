# Peons — documentation technique

Un clawd par session Claude Code, dans une fenêtre transparente posée sur une île. Pour la présentation : [`README.md`](../README.md). Licences, origine des images et de la police : `LICENSE` et `UPSTREAM.md`.

## Construire et installer

```powershell
.\build.ps1                    # tests, puis peons.exe
.\build.ps1 -Out C:\Outils\peons.exe
.\build.ps1 -SignWith <empreinte>   # signature Authenticode (Cert:\CurrentUser\My)
```

- **Un seul fichier** à déployer : `peons.exe`, sans DLL à côté. Il ne dépend que des DLL de Windows, chargées depuis System32 seulement (`windows.NewLazySystemDLL` : une DLL du même nom posée à côté de l'exe ou dans le dossier courant n'est jamais prise). Il écrase la sortie : `build.ps1` échoue si l'exe tourne, d'où `-Out` vers un autre dossier pour tester.
- `-SignWith` : horodatage best-effort (sans, si le serveur est injoignable). Tant que l'éditeur du certificat n'est pas approuvé sur la machine, le statut reste `UnknownError`.
- Windows : l'exécutable est graphique (`-H=windowsgui`), un double-clic n'ouvre aucune console. Une erreur de démarrage s'affiche dans une boîte de message et dans `peons.log`. `-trace` rattache la console parente.
- **Double-clic** : lance l'application et ouvre les réglages. Les hooks viennent du plugin (`plugin/hooks/hooks.json`, voir le README) ; chaque événement est transmis par le hook, qui démarre lui-même l'application (sans réglages) si elle ne tourne pas. Un second lancement envoie l'événement de contrôle `peons.settings` par la socket et rouvre les réglages de l'instance en cours. Pas de lancement au démarrage de Windows : le hook s'en charge.
- **Emplacement** : les hooks du plugin appellent `%LOCALAPPDATA%\peons\peons.exe` par `cmd.exe /c` (la forme sans shell de Claude Code n'expanse pas `%VAR%`). `plugin/bin/fetch.cmd` y installe la dernière release, SHA-256 vérifié ; `/peons:update` le refait en mode forcé. Si `settings.json` contient d'anciens hooks (`claudecfg.LegacyCommand`), l'application le signale au démarrage et dans les réglages ; elle n'écrit plus jamais dans ce fichier.
- **Mises à jour du plugin** : deux, indépendantes. *Le plugin* (hooks, skills) se met à jour depuis le dépôt, mais la mise à jour automatique est **désactivée par défaut** pour une marketplace tierce : à activer dans `/plugin` → Marketplaces → `peons` → Enable auto-update, ou dans `~/.claude/settings.json` (`"extraKnownMarketplaces": {"peons": {"source": {"source": "github", "repo": "aero-md/peons"}, "autoUpdate": true}}`). Claude Code rafraîchit la marketplace peu après le premier message de la session ; la nouvelle version se charge au lancement suivant. Le manifeste ne fixe pas de `version` : chaque commit est une version. À la main : `claude plugin update peons@peons`. *`peons.exe`* ne se met à jour que sur demande (`/peons:update` ; l'ancien exe devient `peons.old.exe`, Windows refusant d'écraser un exe qui tourne).
- **Depuis les sources** : `.\build.ps1 -Out "$env:LOCALAPPDATA\peons\peons.exe"` (tests, puis exe à l'emplacement attendu par les hooks), puis `claude plugin marketplace add .` et `claude plugin install peons@peons` pour installer le plugin depuis la copie locale.

## Commandes

| Commande | Rôle |
|---|---|
| `peons` / `peons run` | lance l'application et ouvre les réglages |
| `peons hook` | appelé par Claude Code, jamais à la main : JSON de l'événement sur stdin (8 Mo au plus), rien sur stdout, code de sortie toujours 0 |

Options de `run` (les durées de cycle et `-ui` priment sur les réglages, pour ce lancement seulement) :

| Option | Effet |
|---|---|
| `-ui N` | pixels d'écran par pixel de sprite (défaut 3) |
| `-happy`, `-neutral`, `-sad` | durées du cycle (défauts 2 min, 5 min, 10 min) |
| `-background` | sans fenêtre de réglages (c'est ainsi que le hook lance l'application) |
| `-demo` | trois bonhommes de démonstration (clic gauche : travaille, clic droit : attend, clic du milieu : s'en va), sans écouter les hooks ; `-work` : durée du « travail » après un clic gauche (défaut 3 s) |
| `-pin <corps>` | un seul bonhomme figé dans `normal`, `alert`, `sleeping`, `bench`, `mining` ou `compacting`, pour vérifier un sprite ; implique `-demo` |
| `-dx N` | décale la fenêtre de N pixels (négatif : vers la gauche), pour montrer plusieurs copies `-pin` côte à côte |
| `-tray=false` | sans icône de zone de notification |
| `-secs N` | quitte après N secondes |
| `-trace` | affiche chaque changement d'aspect (garde la console) |
| `-stats 10s` | journalise l'état du tas Go à cette cadence |

Fichiers de l'utilisateur : `prefs.json` dans le dossier de configuration (`%AppData%\peons`), `peons.log` (tronqué au-delà de 64 Ko) et la socket dans le dossier de cache (`%LocalAppData%\peons`). Pour tester sans les écraser, lancer avec `APPDATA`, `LOCALAPPDATA` et `CLAUDE_CONFIG_DIR` pointant vers un dossier temporaire.

## Interactions

- **Glisser** l'overlay le déplace ; la position est retenue (`prefs.json`). Lâché sous la barre des tâches (où qu'elle soit, quelle que soit sa taille) ou hors de l'écran, il se replace entièrement dans la zone utilisable ; il le fait aussi quand sa taille change (`place.go`, `fit`). Sur la fenêtre de réglages, le premier clic n'est pas avalé par l'activation de la fenêtre (Windows le transmet).
- **Double-clic** sur l'overlay (bonhomme ou herbe) : fenêtre de réglages. Un clic sur un bonhomme le choisit.
- **Icône de la zone de notification** : un clic ouvre le menu **Réglages**, **Toujours afficher**, **Quitter**.
- **Survol d'un bonhomme** (`tooltip_windows.go`) : une étiquette affiche
  - le titre de la session, celui de l'onglet du terminal (nom donné par `/rename`, sinon titre généré, lus dans le journal), coupé à 50 caractères + « … », avec le dossier du projet en sous-titre ; sans titre, le dossier fait titre ;
  - l'état et depuis quand, rafraîchis chaque seconde ;
  - l'outil en cours (`Bash`, `Edit`…), vide hors d'un outil ;
  - une barre de contexte « inversée », de taille fixe (24 segments) quel que soit le texte, qui se remplit avec le contexte utilisé et vire du vert au rouge, avec les tokens restants.

  Le contexte vient du dernier message de Claude dans le journal de la session (`transcript_path`, donné au hook) : tokens = entrée + création de cache + lecture de cache + sortie. La taille de fenêtre n'y figure pas : 200 000 supposés, 1 million pour un modèle `[1m]` ou dès que l'usage dépasse 200 000 (estimation, `internal/transcript`). Le journal n'est relu que si sa taille ou sa date change. Police : PxPlus IBM EGA 8x14 (`assets/`), dessinée à 16 px × k pour rester nette. k = arrondi(échelle de l'écran × taille de l'étiquette), au moins 1 (`tipScale`). La taille de l'étiquette (1× à 4×, 1× par défaut, `tip_scale` dans `prefs.json`) se règle à part dans la fenêtre de réglages et ne dépend pas de celle des clawd.

## Fenêtre de réglages

Une vraie fenêtre Windows (`settings_windows.go`), créée à l'ouverture et détruite à la fermeture (rien en mémoire fermée), rafraîchie chaque seconde. Tout est dessiné par Go (`gofont`) dans une section DIB que `BitBlt` recopie à l'écran : ni GPU ni cache de textures. Échap ou la croix la ferme ; la molette la fait défiler. Elle montre :

- **Claude Code** : avertissement si d'anciens hooks de peons traînent dans `settings.json` ;
- **Toujours afficher**, même sans session (sinon l'overlay se cache) ;
- **Taille des clawd** (− / +, 1× à 8×) : l'île et la fenêtre suivent ;
- **Taille de l'étiquette** (− / +, 1× à 4×) : l'étiquette de survol, indépendante de la taille des clawd ;
- **Afficher l'île** : décochée, la fenêtre se réduit à la zone où dessinent les clawd, sans marge, jusqu'à la ligne des pieds ; lâchés contre un bord ou la barre des tâches, ils s'y calent pieds sur la barre ; le bas de la roche du mineur, qui s'enfonce dans l'herbe, est coupé au sol ; les clawd ne bougent pas quand on bascule. Sans session, rien n'est affiché, même avec « Toujours afficher » ;
- **Taille de l'île automatique** ou **Sections de l'île** (1 à 60) ;
- **Content pendant**, **Neutre pendant**, **Triste pendant** (minutes) ;
- les **sessions** (état et durée) et le **détail** de la session choisie : humeur, outil, dernier contexte, derniers événements (6 au plus) ;
- **Quitter peons**.

Tout va dans `prefs.json` (`Scale`, `HappyMin`, `NeutralMin`, `SadMin`, `AlwaysShow`, `Placed`, île, position) et s'applique tout de suite.

## États d'un bonhomme

`internal/mood` est une fonction pure du temps : pas de timer, pas d'état caché.

| Activité (événement Claude Code) | Corps | Détail |
|---|---|---|
| démarrage (`SessionStart`) ou tour fini sur une erreur (`StopFailure`) | `normal`, yeux neutres | rien à fêter ; un nouveau venu apparaît par un portail |
| Claude travaille (`UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionDenied`) | `mining` | pioche en main, roche à droite, 2 frames à 300 ms, yeux de profil propres à chaque frame |
| Claude attend une réponse ou une permission (`PermissionRequest`) | `alert` | panneau « ! », 2 frames à 350 ms |
| Claude compacte son contexte (`PreCompact`) | `compacting` | un piston écrase le clawd, 2 frames à 500 ms |
| réponse finie (`Stop`) | `normal`, yeux **content** pendant 2 min | puis neutre |
| inactivité | neutre 5 min, **triste** 10 min | puis endormi jusqu'à la prochaine sollicitation |
| endormi | `sleeping`, bulle de sommeil | 2 frames à 1,2 s ; il se réveille pour le banc (`bench`, barre qui monte et descend toutes les 500 ms) 20 s toutes les 15 min de sommeil, puis se rendort |

```
démarrage ───────────────────────── neutre ─┐
réponse finie ─ content ─ (2 min) ─ neutre ─┴─ (5 min) ─ triste ─ (10 min) ─ endormi ─ … ─ prochaine sollicitation
```

- **Roche du mineur** : elle se fend en cinq étapes de 12 s, soit une minute, puis explose en éclats pendant que le mineur se fige, pioche baissée ; une roche neuve tombe du haut de la fenêtre et il reprend. Cycle d'environ 61 s, qui repart d'une roche intacte à chaque reprise du travail. Le figement vient du champ `hold` d'une frame d'effet (voir plus bas).
- **Compactage** : la 2ᵉ frame est le même corps écrasé de 7 lignes (`squash` du manifest : `rows`, `keep_top`, `keep_bottom` ; retire d'abord les lignes qui répètent celle du dessus pour garder les bras).
- **Fin du travail** : `Stop`. Un tour interrompu avec Échap n'envoie **aucun** hook ; sans parade, le bonhomme creuserait pour toujours. L'annulation ne laisse de trace que dans le journal (`[Request interrupted by user]`), relu chaque seconde pendant le travail : le bonhomme arrête de creuser après 5 s sans hook (le temps qu'un nouveau prompt, qui envoie un hook, reprenne la main sans clignotement). Filet ultime : sans nouvelle pendant 15 min, le travail est jugé fini. Le compactage a la même limite de 15 min.
- **Sous-agents** : chaque sous-agent vivant (outil Agent/Task) est une petite particule qui flotte de ±1 px à son propre rythme au-dessus de la tête de son clawd parent, en ligne centrée (resserrée pour tenir dans la fenêtre) ; elle ne change pas le corps du parent. Trois états : *working* (utilise un outil, ou actif depuis moins de 10 s), *idle* (muet depuis plus de 10 s), *throttle* (la session principale a fini son tour et l'attend). Les événements d'un sous-agent portent le `session_id` du parent plus `agent_id` / `agent_type`. Un sous-agent muet depuis 2 min est oublié (filet si son `SubagentStop` n'arrive jamais). Sprites : `assets/fx/agent_{idle,working,throttle}.png`.
- **Apparition** : un portail ovale s'ouvre et le clawd en tombe, droit et neutre (900 ms au total : ouverture jusqu'à 280 ms, chute de 200 à 650 ms, fermeture à partir de 680 ms).
- **Disparition** : une session s'en va à `SessionEnd`, quand son processus Claude Code meurt (vérifié toutes les 3 s ; le PID est repéré au `SessionStart` et au `UserPromptSubmit` pour détecter un terminal fermé, qui n'envoie aucun `SessionEnd`), ou après 2 h sans événement. Son clawd se désagrège alors à sa place, figé dans sa dernière pose (`internal/snap`, 2,5 s) : 45 % des pixels s'effacent sur place, au hasard, dans la première seconde ; les autres se détachent, le côté droit d'abord, s'envolent avec le vent vers la droite et le haut, virent à la poussière et s'effacent en vol. Les autres bonhommes se resserrent ensuite.
- **Événements écoutés** (`plugin/hooks/hooks.json`) : `UserPromptSubmit`, `SessionStart`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionDenied`, `StopFailure`, `PermissionRequest` (matcher `*` pour `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionDenied` et `PermissionRequest`), `PreCompact` (matchers `auto` et `manual`), `Stop`, `SubagentStop`, `SessionEnd`.

## Disposition

- **L'île est faite de morceaux dessinés** (`assets/terrain`) : extrémité gauche, autant de sections centrales qu'il faut, extrémité droite. Chaque section prend au hasard l'une des 5 variantes, tirée une fois pour toutes (agrandir l'île ne change pas les sections déjà posées). Elles se raccordent dans n'importe quel ordre et aux deux extrémités (même silhouette aux jointures, vérifiée par un test). `terrain.json` fixe la ligne des pieds (`feet`) et la zone de garde (`guard`, 12 px).
- **Les corps sont centrés sur l'île**, à au moins `guard` pixels de chaque bord pour qu'aucun ne se tienne sur la pente de l'extrémité. L'île automatique est la plus petite qui garantisse cette zone ; une île manuelle trop courte les laisse déborder. La fenêtre contient l'île, tout ce que dessinent les bonhommes (la bulle du dernier dépasse à droite) et 4 px de marge (`geometry.go`).
- **Les clawd sont serrés au plus près sans se toucher** : l'écart d'un corps au suivant (`avatar.Spacing`) est calculé au chargement, ligne par ligne, sur tout ce qu'un clawd peut dessiner (bulle, banc, roche, panneau…) : au moins un pixel vide entre voisins, quelles que soient leurs poses, même en diagonale. La bulle de l'un peut ainsi passer au-dessus du suivant. Un sprite plus large écarte les clawd sans toucher au code.
- Au plus **8 bonhommes affichés** (les plus anciens cèdent la place) et **32 sessions suivies** (la plus silencieuse est oubliée au-delà).

## Le personnage (`assets/characters/clawd`)

- **Corps et yeux sont séparés** : un corps (2 images qui alternent), une paire d'yeux par émotion (`neutral`, `happy`, `sad`, `sleeping`), des effets (`fx`) : bulle de sommeil, pioche, bras, roche, piston, banc et barre, panneau d'attente, portail, particules de sous-agents. Chaque corps ancre ses yeux par frame (l'ancre est le centre horizontal de la paire) ; les corps qui ont leurs propres yeux (`mining`, `bench`) les donnent par `eyes.image`, qui remplace l'émotion. Ordre de dessin : corps, yeux, puis effets (la pioche passe devant le visage). Les coordonnées de main sont dans l'image du corps.
- **Les images d'effet vivent dans `assets/fx`**, à la racine des assets (`avatar.FXDir`) : dans un bloc `fx` du manifest, un nom nu (`"rock.png"`) se lit là ; un nom avec dossier (`"body/mining_arm.png"`, les bras du mineur) reste relatif au dossier du personnage. `portal.png` et `agent_*.png` y sont aussi.
- **Un rythme par animation** : `period` en ms (`bodies.<nom>.period`, `fx.<nom>.period`, 600 par défaut). Chaque animation compte le temps depuis son propre démarrage : en ajouter une n'accélère pas les autres.
- **Une frame d'effet peut avoir sa durée** (`ms`) **et figer le reste** (`hold`) : tant qu'elle est affichée, l'horloge du corps et des autres effets s'arrête, puis repart d'où elle était (le mineur figé pendant l'explosion de sa roche ; un seul effet par corps peut le faire).
- Une frame de corps peut poser une image plus petite dans une toile à la taille du personnage (`canvas`, utilisé par le banc). Des coordonnées `y` négatives sont relatives à l'image du corps. De nouveaux calques (accessoires) s'ajoutent au manifest sans toucher au code.
- Les sprites sont embarqués au build : **rebuild après avoir changé une image ou le manifest**.

## Architecture

```
cmd/peons             commandes run / hook ; Win32 (win32_windows.go), boucle de messages, overlay, réglages, tray, étiquette
internal/session      Store des sessions : événements des hooks → mood.Snapshot, sous-agents, contexte, titre
internal/mood         Snapshot + temps → aspect (corps, émotion) ; fonction pure
internal/avatar       manifest du personnage : corps, yeux, fx, squash ; horloge propre à chaque animation ; Spacing / Reach
internal/ipc          socket Unix (Windows 10+, Linux, macOS) entre hook et application
internal/claudecfg    détection d'anciens hooks dans settings.json (lecture seule)
internal/proc         retrouve le processus Claude Code et vérifie qu'il vit (par OS)
internal/transcript   contexte et titre, lus dans le journal JSONL d'une session
internal/terrain      assemble l'île : extrémité gauche, sections centrales au hasard, extrémité droite
internal/snap         désagrège un bonhomme qui s'en va, pixel par pixel (2,5 s)
internal/prefs        réglages : position, taille, durées, île, affichage sans session
assets/characters     personnages embarqués (clawd) : sprites + manifest.json
assets/fx             effets du personnage (roche, pioche, piston, banc, portail, particules d'agents…)
assets/terrain        morceaux de l'île (gauche, 5 variantes de centre, droite) + terrain.json
tools/extract-eyes    sépare yeux et corps d'un sprite complet (n'écrase rien sans -force)
tools/readme-gifs     rend les GIF du README (`docs/img`) avec les vrais assets : `go run ./tools/readme-gifs` ; `-out <dossier>` ; `-dump <dossier>` écrit un PNG par image de chaque GIF
tools/rock-fx         tire de rock.png les frames du cycle (réécrit `assets/fx/rock_*.png` et l'entrée `mining_rock` du manifest ; `-assets` pour la racine des assets, `-seed` pour d'autres fissures ; à relancer après retouche)
tools/mining-gif      rend en GIF le clawd qui creuse, roche figée (`go run ./tools/mining-gif art/mining.gif`)
tools/portaltex       recrée la texture du portail d'apparition (d'après la palette d'une image de référence, non versionnée)
tools/exe-icon        écrit cmd/peons/rsrc_windows_amd64.syso, l'icône de peons.exe (le clawd), liée par go build ; à relancer et à commiter quand le sprite change
tools/soak            test de charge contre les fuites mémoire
```

### Choix techniques

- **Windows uniquement, Win32 direct** : aucune bibliothèque d'interface tierce. Les appels passent par `golang.org/x/sys/windows` (sous-dépôt du projet Go) et les DLL système (`user32`, `gdi32`, `shell32`), CGO off.
- **Overlay natif sous Windows** (`overlay_windows.go`) : fenêtre `WS_EX_LAYERED | TOPMOST | TOOLWINDOW | NOACTIVATE` dont l'image BGRA prémultipliée est composée en Go (sprites agrandis au plus proche) et présentée par `UpdateLayeredWindow`. L'alpha de chaque pixel décide aussi du clic : un pixel transparent laisse passer la souris. Ni GPU, ni région, ni composition DWM : pas de clignotement au changement de forme ni de teinte plaquée au changement de focus ; environ 20 Mo de mémoire privée. Les entrées souris passent par la procédure de fenêtre (`CS_DBLCLKS`, `SetCapture`). Le fil principal est verrouillé (`LockOSThread`) : toutes les fenêtres et la pompe de messages restent sur lui.
- **Boucle de messages** : elle dort (`MsgWaitForMultipleObjectsEx`) jusqu'au prochain changement utile (frame d'animation, changement d'humeur, vérification de processus) ou jusqu'à un message. Les goroutines (IPC, minuteurs de démo) la réveillent par `wake()` : un `PostMessage` vers une fenêtre cachée (`peonsMain`), qui porte aussi l'icône de la zone de notification ; les réveils en rafale sont fusionnés. Les procédures de fenêtre ne font que noter ce qui est demandé (entrées de l'overlay, `pendingActions`) ; la boucle l'applique à son passage. Sans session, la fenêtre est cachée et le processus ne consomme rien. L'IPC n'écoute qu'une fois la fenêtre principale créée (`wake` n'a rien à réveiller avant). Le processus est sensible au DPI de chaque écran (`SetProcessDpiAwarenessContext`) : toutes les coordonnées sont des pixels physiques.
- **IPC** : socket Unix par utilisateur, pas de port TCP. Entrée non fiable : 16 Ko, 2 s et 16 connexions simultanées au plus, champs validés. Le hook lance l'application détachée si personne n'écoute (`startDetached`), puis réessaie jusqu'à 3 s ; il n'en lance pas pour un `SessionEnd`.
- **Ordre des événements** : chaque hook est un processus distinct, et selon le shell il peut ne pas être attendu (l'exécutable est graphique) ; le hook horodate donc chaque événement (`At`) et l'application ignore un événement plus ancien que le dernier appliqué pour la même session.
- **Mémoire bornée** : sessions et bonhommes plafonnés, historique de 6 événements, textures créées une fois (images immuables), tas rendu au système 20 s après la dernière rafale (`debug.FreeOSMemory`). Mesures sous Windows : environ 20 Mo privés au repos, 0 % de CPU sans session ; `tools/soak` envoie des dizaines de milliers d'événements pour le vérifier ; le tas Go plafonne à environ 5 Mo et goroutines et handles restent stables.
- **Pièges connus** : dans `main`, ne pas écrire `cmd, os.Args = os.Args[1], append(os.Args[:1], …)` (ordre d'évaluation indéfini)  ; en démo ou en test, lancer l'application avec un dossier temporaire **court** (`APPDATA`, `LOCALAPPDATA`, `XDG_RUNTIME_DIR`) : le chemin de la socket Unix ne peut pas dépasser 108 caractères. Le canevas des réglages est la mémoire de la section DIB (BGRA) lue comme du RGBA : les couleurs y sont écrites rouge et bleu échangés (`bgr`).
- **Tests** : `go test ./...` couvre l'humeur, les sessions, l'IPC, `claudecfg`, l'avatar, l'île et la disposition. Pour tester le hook à la main, lancer `peons hook` avec `Start-Process -PassThru` + `WaitForExit()` (pas `-Wait`, qui attend aussi l'application lancée par le hook).

## Clic sur un bonhomme

`proc.Focus(pid, titre)` retrouve la fenêtre du terminal par la console de Claude Code (`AttachConsole` + `GetConsoleWindow` + `GetAncestor(GA_ROOTOWNER)`), la met au premier plan et la fait clignoter. Dans Windows Terminal (un seul processus pour toutes les fenêtres), l'onglet est ensuite sélectionné par UI Automation (`uia_windows.go`, `CUIAutomation` appelé par les vtables COM) : on liste les `TabItem` de la fenêtre, on compare leur nom au titre de la session (`SameTitle`, qui ignore le symbole d'état en tête) et on appelle `SelectionItemPattern.Select()`. Windows Terminal ne relie un onglet ni à un PID ni à `WT_SESSION` : le titre est la seule jonction, elle peut être tronquée, dupliquée ou changée à la main. Sans correspondance, la fenêtre reste seule au premier plan.
