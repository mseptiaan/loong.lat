package platform

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEnvReadsTypedValuesWithDefaults(t *testing.T) {
	env := NewEnvFrom(map[string]string{
		"HTTP_ADDR": ":9000", "WORKERS": "8", "DEBUG": "true", "TIMEOUT": "45s",
		"BROKERS": " a:1, b:2 ,,c:3", "BLANK": "",
	})
	if got := env.String("HTTP_ADDR", ":8081"); got != ":9000" {
		t.Errorf("String = %q", got)
	}
	if got := env.String("MISSING", "fallback"); got != "fallback" {
		t.Errorf("String default = %q", got)
	}
	if got := env.String("BLANK", "fallback"); got != "fallback" {
		t.Errorf("an empty variable counts as unset, got %q", got)
	}
	if got := env.Int("WORKERS", 1); got != 8 {
		t.Errorf("Int = %d", got)
	}
	if got := env.Bool("DEBUG", false); !got {
		t.Errorf("Bool = %v", got)
	}
	if got := env.Duration("TIMEOUT", time.Second); got != 45*time.Second {
		t.Errorf("Duration = %v", got)
	}
	if got := env.List("BROKERS"); !slices.Equal(got, []string{"a:1", "b:2", "c:3"}) {
		t.Errorf("List = %q", got)
	}
	if got := env.List("MISSING", "localhost:19092"); !slices.Equal(got, []string{"localhost:19092"}) {
		t.Errorf("List default = %q", got)
	}
	if err := env.Err(); err != nil {
		t.Errorf("Err = %v", err)
	}
}

func TestEnvReportsEveryProblemAtOnce(t *testing.T) {
	env := NewEnvFrom(map[string]string{"WORKERS": "many", "DEBUG": "maybe", "TIMEOUT": "soon"})
	env.Require("DATABASE_URL")
	if got := env.Int("WORKERS", 3); got != 3 {
		t.Errorf("a bad value falls back to the default, got %d", got)
	}
	env.Bool("DEBUG", false)
	env.Duration("TIMEOUT", time.Second)

	err := env.Err()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, key := range []string{"DATABASE_URL", "WORKERS", "DEBUG", "TIMEOUT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not mention %s", err, key)
		}
	}
}

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := `# local development settings
HTTP_ADDR=:9100
export LOG_LEVEL=debug
QUOTED="two words"
SINGLE='it is'
COMMENTED=value # trailing comment
EMPTY=
ALREADY_SET=from-file
  SPACED  =  padded  
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// Register every key with t.Setenv first so the test restores the environment afterwards.
	for _, key := range []string{"HTTP_ADDR", "LOG_LEVEL", "QUOTED", "SINGLE", "COMMENTED", "EMPTY", "SPACED"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("ALREADY_SET", "from-process")

	if err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HTTP_ADDR": ":9100", "LOG_LEVEL": "debug", "QUOTED": "two words", "SINGLE": "it is",
		"COMMENTED": "value", "ALREADY_SET": "from-process", "SPACED": "padded",
	}
	for key, value := range want {
		if got := os.Getenv(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if _, set := os.LookupEnv("EMPTY"); set {
		t.Error("an empty value must not be exported")
	}
}

func TestLoadDotEnvToleratesAMissingFileButNotAMalformedOne(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Errorf("a missing file is normal outside local development: %v", err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("GOOD=1\nthis is not a setting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOD", "")
	err := LoadDotEnv(path)
	if err == nil || !strings.Contains(err.Error(), ":2:") {
		t.Errorf("want an error naming line 2, got %v", err)
	}
}
