package mood

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func TestResolve(t *testing.T) {
	c := Config{HappyFor: 2 * time.Minute, NeutralFor: 3 * time.Minute, SadFor: 10 * time.Minute}
	tests := []struct {
		name string
		s    Snapshot
		at   time.Duration
		want Look
	}{
		{"démarrage", Snapshot{Idle, t0, false, false}, 0, Look{BodyNormal, Neutral}},
		{"démarrage sans réponse reste neutre", Snapshot{Idle, t0, false, false}, time.Minute, Look{BodyNormal, Neutral}},
		{"démarrage : neutre pendant content + neutre", Snapshot{Idle, t0, false, false}, 5*time.Minute - time.Second, Look{BodyNormal, Neutral}},
		{"démarrage : triste ensuite", Snapshot{Idle, t0, false, false}, 5 * time.Minute, Look{BodyNormal, Sad}},
		{"réponse finie", Snapshot{Idle, t0, true, false}, 0, Look{BodyNormal, Happy}},
		{"encore content", Snapshot{Idle, t0, true, false}, 2*time.Minute - time.Second, Look{BodyNormal, Happy}},
		{"neutre après content, pas triste d'emblée", Snapshot{Idle, t0, true, false}, 2 * time.Minute, Look{BodyNormal, Neutral}},
		{"encore neutre", Snapshot{Idle, t0, true, false}, 5*time.Minute - time.Second, Look{BodyNormal, Neutral}},
		{"triste à la limite", Snapshot{Idle, t0, true, false}, 5 * time.Minute, Look{BodyNormal, Sad}},
		{"triste sans pleurer", Snapshot{Idle, t0, true, false}, 14 * time.Minute, Look{BodyNormal, Sad}},
		{"endormi à la limite", Snapshot{Idle, t0, true, false}, 15 * time.Minute, Look{BodySleeping, Sleeping}},
		{"endormi indéfiniment", Snapshot{Idle, t0, true, false}, 1000 * time.Hour, Look{BodySleeping, Sleeping}},
		{"démarrage sans réponse finit aussi par dormir", Snapshot{Idle, t0, false, false}, 15 * time.Minute, Look{BodySleeping, Sleeping}},
		{"travail", Snapshot{Working, t0, false, false}, 1000 * time.Hour, Look{BodyMining, Neutral}},
		{"compactage", Snapshot{Compacting, t0, false, false}, time.Hour, Look{BodyCompact, Neutral}},
		{"attend une réponse", Snapshot{Awaiting, t0, false, false}, 1000 * time.Hour, Look{BodyAlert, Neutral}},
	}
	for _, tt := range tests {
		if got := c.Resolve(tt.s, t0.Add(tt.at)); got != tt.want {
			t.Errorf("%s : %v, attendu %v", tt.name, got, tt.want)
		}
	}
}

func TestNextChange(t *testing.T) {
	c := Config{HappyFor: 2 * time.Minute, NeutralFor: 3 * time.Minute, SadFor: 10 * time.Minute}
	idle := Snapshot{Idle, t0, true, false}
	tests := []struct {
		name string
		s    Snapshot
		at   time.Duration
		want time.Time
	}{
		{"content → neutre", idle, 30 * time.Second, t0.Add(2 * time.Minute)},
		{"neutre → triste", idle, 3 * time.Minute, t0.Add(5 * time.Minute)},
		{"démarrage : neutre → triste, sans échéance à la fin de content", Snapshot{Idle, t0, false, false}, 30 * time.Second, t0.Add(5 * time.Minute)},
		{"triste → endormi", idle, 6 * time.Minute, t0.Add(15 * time.Minute)},
		{"endormi : plus rien", idle, 16 * time.Minute, time.Time{}},
		{"travail : rien", Snapshot{Working, t0, false, false}, time.Minute, time.Time{}},
		{"alerte : rien", Snapshot{Awaiting, t0, false, false}, time.Minute, time.Time{}},
	}
	for _, tt := range tests {
		if got := c.NextChange(tt.s, t0.Add(tt.at)); !got.Equal(tt.want) {
			t.Errorf("%s : %v, attendu %v", tt.name, got, tt.want)
		}
	}
}

// Endormi, il se réveille pour le banc puis se rendort, en boucle, à partir de
// l'endormissement (12 min ici).
func TestBenchCycle(t *testing.T) {
	c := Config{HappyFor: 2 * time.Minute, SadFor: 10 * time.Minute, SleepFor: 15 * time.Minute, BenchFor: 20 * time.Second}
	idle := Snapshot{Idle, t0, true, false}
	sleeping, bench := Look{BodySleeping, Sleeping}, Look{BodyBench, Neutral}
	tests := []struct {
		at        time.Duration
		want      Look
		nextAfter time.Duration // prochaine échéance, depuis t0
	}{
		{12 * time.Minute, sleeping, 27 * time.Minute},
		{27*time.Minute - time.Nanosecond, sleeping, 27 * time.Minute},
		{27 * time.Minute, bench, 27*time.Minute + 20*time.Second},
		{27*time.Minute + 19*time.Second, bench, 27*time.Minute + 20*time.Second},
		{27*time.Minute + 20*time.Second, sleeping, 42*time.Minute + 20*time.Second},
		{42*time.Minute + 30*time.Second, bench, 42*time.Minute + 40*time.Second},
	}
	for _, tt := range tests {
		now := t0.Add(tt.at)
		if got := c.Resolve(idle, now); got != tt.want {
			t.Errorf("à %v : %v, attendu %v", tt.at, got, tt.want)
		}
		if got := c.NextChange(idle, now); !got.Equal(t0.Add(tt.nextAfter)) {
			t.Errorf("à %v : prochaine échéance %v, attendue %v", tt.at, got.Sub(t0), tt.nextAfter)
		}
	}
	if got := c.Resolve(Snapshot{Working, t0, false, false}, t0.Add(27*time.Minute)); got.Body != BodyMining {
		t.Errorf("au travail, pas de banc : %v", got)
	}
}

// La prochaine échéance annoncée doit bien être un changement d'aspect, banc
// compris, après une réponse comme au démarrage.
func TestNextChangeIsRealChangeWithBench(t *testing.T) {
	c := DefaultConfig
	for _, s := range []Snapshot{{Idle, t0, true, false}, {Idle, t0, false, false}} {
		now := t0
		for i := 0; i < 20; i++ {
			next := c.NextChange(s, now)
			if next.IsZero() {
				t.Fatalf("répondu=%v, échéance %d : plus rien alors que le banc revient", s.Answered, i)
			}
			if before, after := c.Resolve(s, next.Add(-time.Nanosecond)), c.Resolve(s, next); before == after {
				t.Fatalf("répondu=%v, échéance %v sans changement d'aspect (%v)", s.Answered, next.Sub(t0), before)
			}
			now = next
		}
	}
}

// Sans banc, la chaîne d'échéances finit dans le sommeil.
func TestNextChangeIsRealChange(t *testing.T) {
	c := DefaultConfig
	c.BenchFor = 0
	for _, s := range []Snapshot{{Idle, t0, true, false}, {Idle, t0, false, false}} {
		now, done := t0, false
		for i := 0; i < 6 && !done; i++ {
			next := c.NextChange(s, now)
			if next.IsZero() {
				done = true
				break
			}
			before, after := c.Resolve(s, next.Add(-time.Nanosecond)), c.Resolve(s, next)
			if before == after {
				t.Fatalf("répondu=%v, échéance %v sans changement d'aspect (%v)", s.Answered, next.Sub(t0), before)
			}
			now = next
		}
		if !done {
			t.Fatalf("répondu=%v : la chaîne d'échéances ne se termine pas", s.Answered)
		}
	}
}

// Le cycle par défaut passe par les quatre humeurs, dans l'ordre, sans en sauter.
func TestDefaultCycleOrder(t *testing.T) {
	c := DefaultConfig
	s := Snapshot{Idle, t0, true, false}
	var seen []Emotion
	for now := t0; ; {
		if e := c.Resolve(s, now).Emotion; len(seen) == 0 || seen[len(seen)-1] != e {
			seen = append(seen, e)
		}
		if seen[len(seen)-1] == Sleeping {
			break
		}
		now = c.NextChange(s, now)
	}
	want := []Emotion{Happy, Neutral, Sad, Sleeping}
	if len(seen) != len(want) {
		t.Fatalf("cycle %v, attendu %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("cycle %v, attendu %v", seen, want)
		}
	}
}

// Attendre un agent de fond : content reste content, le reste est plafonné à neutre.
func TestWaitingFloorsAtNeutral(t *testing.T) {
	c := DefaultConfig
	tests := []struct {
		name     string
		answered bool
		at       time.Duration
		want     Look
	}{
		{"content s'il l'était", true, time.Minute, Look{BodyNormal, Happy}},
		{"content fini : neutre", true, 2 * time.Minute, Look{BodyNormal, Neutral}},
		{"serait triste : neutre", true, 10 * time.Minute, Look{BodyNormal, Neutral}},
		{"serait endormi : neutre", true, 30 * time.Minute, Look{BodyNormal, Neutral}},
		{"serait sur le banc : neutre", true, 15*time.Minute + 15*time.Minute + 5*time.Second, Look{BodyNormal, Neutral}},
		{"sans réponse, jamais content", false, time.Second, Look{BodyNormal, Neutral}},
		{"sans réponse, endormi : neutre", false, 1000 * time.Hour, Look{BodyNormal, Neutral}},
	}
	for _, tt := range tests {
		s := Snapshot{Idle, t0, tt.answered, true}
		if got := c.Resolve(s, t0.Add(tt.at)); got != tt.want {
			t.Errorf("%s : %+v, attendu %+v", tt.name, got, tt.want)
		}
	}
	happy := Snapshot{Idle, t0, true, true}
	if got := c.NextChange(happy, t0.Add(30*time.Second)); !got.Equal(t0.Add(c.HappyFor)) {
		t.Errorf("content : prochaine échéance %v, attendu la fin de content", got)
	}
	for _, s := range []Snapshot{{Idle, t0, true, true}, {Idle, t0, false, true}} {
		if got := c.NextChange(s, t0.Add(c.HappyFor+time.Second)); !got.IsZero() {
			t.Errorf("neutre en attente : échéance %v, attendu aucune", got)
		}
	}
	// l'attente finie, le cycle normal reprend (là où l'horloge en est)
	if got := c.Resolve(Snapshot{Idle, t0, true, false}, t0.Add(10*time.Minute)); got.Emotion != Sad {
		t.Errorf("sans attente : %+v, attendu triste", got)
	}
}
