// Package mood décide de l'aspect d'un bonhomme (corps + émotion) à partir de
// ce que fait sa session et depuis combien de temps. C'est une fonction pure du
// temps : pas de timer, pas d'état caché, donc testable et rejouable.
//
// Cycle d'une session :
//
//	démarrage ────────────────────────── neutre ─┐
//	réponse finie ─ happy ─ (HappyFor) ─ neutre ─┴─ (NeutralFor) ─ triste ─ (SadFor) ─ endormi ─ … ─ sollicitation
//
// Tant que Claude attend un agent en arrière-plan (Snapshot.Waiting), le cycle est
// gelé au plancher « neutre » : content reste content jusqu'à HappyFor puis neutre,
// triste et endormi repassent neutre, et rien ne descend plus bas.
//
// Endormi, il se réveille de temps en temps pour soulever de la fonte : BenchFor
// de banc toutes les SleepFor de sommeil, puis il se rendort.
//
// Dès qu'on le sollicite (prompt, outil, question), le bonhomme se réveille.
package mood

import "time"

// Activity est ce que fait la session au moment de Snapshot.Since.
type Activity int

const (
	Idle       Activity = iota // rien en cours (démarrage ou réponse terminée)
	Working                    // Claude travaille
	Awaiting                   // Claude attend une réponse de l'utilisateur
	Compacting                 // Claude compacte son contexte
)

// Body est la silhouette à dessiner ; les noms sont ceux du manifest du personnage.
type Body string

const (
	BodyNormal   Body = "normal"
	BodyMining   Body = "mining"     // Claude travaille : le clawd creuse
	BodyCompact  Body = "compacting" // Claude compacte son contexte : un piston écrase le clawd
	BodyAlert    Body = "alert"
	BodySleeping Body = "sleeping"
	BodyBench    Body = "bench" // endormi depuis longtemps : le clawd se réveille et fait du banc
)

// Emotion choisit les yeux ; les noms sont ceux du manifest du personnage.
type Emotion string

const (
	Neutral  Emotion = "neutral"
	Happy    Emotion = "happy"
	Sad      Emotion = "sad"
	Sleeping Emotion = "sleeping"
)

// Look est l'aspect résolu.
type Look struct {
	Body    Body
	Emotion Emotion
}

// Snapshot décrit l'état d'une session.
type Snapshot struct {
	Activity Activity
	Since    time.Time // début de l'activité courante
	Answered bool      // vrai si Idle parce qu'une réponse vient de se terminer
	// Waiting : Idle, mais Claude attend un agent en arrière-plan. Le bonhomme reste
	// alors content (s'il l'était) ou neutre : jamais triste ni endormi tant qu'on
	// attend (ce champ vient de la session, qui seule connaît les sous-agents).
	Waiting bool
}

// Config fixe la durée des phases d'inactivité.
type Config struct {
	HappyFor   time.Duration // content après une réponse (au démarrage : neutre), avant de redevenir neutre
	NeutralFor time.Duration // neutre, entre content et triste
	SadFor     time.Duration // triste, avant de s'endormir
	// SleepFor, BenchFor : endormi, il dort SleepFor puis fait BenchFor de banc, et
	// recommence ; l'un ou l'autre à 0 : il dort sans se réveiller.
	SleepFor time.Duration
	BenchFor time.Duration
}

// DefaultConfig : valeurs de départ, à ajuster à l'usage.
var DefaultConfig = Config{HappyFor: 2 * time.Minute, NeutralFor: 5 * time.Minute, SadFor: 10 * time.Minute, SleepFor: 15 * time.Minute, BenchFor: 20 * time.Second}

// Resolve donne l'aspect de la session à l'instant now.
func (c Config) Resolve(s Snapshot, now time.Time) Look {
	switch s.Activity {
	case Working:
		return Look{BodyMining, Neutral} // les yeux du corps qui creuse lui sont propres : l'émotion n'y change rien
	case Awaiting:
		return Look{BodyAlert, Neutral}
	case Compacting:
		return Look{BodyCompact, Neutral}
	}
	d := now.Sub(s.Since)
	if s.Waiting { // plancher : neutre. Content jusqu'à HappyFor s'il l'était, jamais triste ni endormi
		if d < c.HappyFor && s.Answered {
			return Look{BodyNormal, Happy}
		}
		return Look{BodyNormal, Neutral}
	}
	switch {
	case d < c.HappyFor && s.Answered:
		return Look{BodyNormal, Happy}
	case d < c.HappyFor+c.NeutralFor:
		return Look{BodyNormal, Neutral}
	case d < c.awake():
		return Look{BodyNormal, Sad}
	}
	if _, bench := c.sleepCycle(d - c.awake()); bench {
		return Look{BodyBench, Neutral} // les yeux du banc sont propres au corps
	}
	return Look{BodySleeping, Sleeping}
}

// awake : durée d'inactivité avant l'endormissement.
func (c Config) awake() time.Duration { return c.HappyFor + c.NeutralFor + c.SadFor }

// sleepCycle donne, après asleep de sommeil, le temps restant avant le prochain
// réveil ou endormissement (0 : jamais) et si le bonhomme est sur le banc.
func (c Config) sleepCycle(asleep time.Duration) (left time.Duration, bench bool) {
	if c.SleepFor <= 0 || c.BenchFor <= 0 {
		return 0, false
	}
	t := asleep % (c.SleepFor + c.BenchFor)
	if t < c.SleepFor {
		return c.SleepFor - t, false
	}
	return c.SleepFor + c.BenchFor - t, true
}

// NextChange donne l'instant où l'aspect changera sans nouvel événement, ou le
// zéro de time.Time s'il ne changera plus : la boucle d'affichage peut dormir
// jusque-là.
func (c Config) NextChange(s Snapshot, now time.Time) time.Time {
	if s.Activity != Idle {
		return time.Time{}
	}
	d := now.Sub(s.Since)
	if s.Waiting { // seule la fin du content change quelque chose ; la fin de l'attente vient de la session
		if d < c.HappyFor && s.Answered {
			return s.Since.Add(c.HappyFor)
		}
		return time.Time{}
	}
	switch {
	case d < c.HappyFor && s.Answered:
		return s.Since.Add(c.HappyFor)
	case d < c.HappyFor+c.NeutralFor: // au démarrage, neutre dès le début : pas d'échéance à HappyFor
		return s.Since.Add(c.HappyFor + c.NeutralFor)
	case d < c.awake():
		return s.Since.Add(c.awake())
	}
	if left, _ := c.sleepCycle(d - c.awake()); left > 0 {
		return now.Add(left)
	}
	return time.Time{}
}
