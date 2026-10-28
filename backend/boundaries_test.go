package backend_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The architecture's one hard rule: services share no code, only wire contracts.
//
//   - services/X may import pkg/* but never services/Y (X != Y);
//   - pkg/* never imports services/*.
//
// The check reads import clauses with go/parser (tests included, build tags ignored), so it needs no build,
// keeps working while another package is mid-edit, and covers services added later without any change here.

const modulePath = "github.com/mseptiaan/loong.lat/backend"

// violations returns one message per forbidden import found under root.
func violations(root string) ([]string, error) {
	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		owner := owner(filepath.ToSlash(rel))
		if owner.kind == "" {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			if problem := check(owner, imported); problem != "" {
				found = append(found, fmt.Sprintf("%s imports %s: %s", filepath.ToSlash(rel), imported, problem))
			}
		}
		return nil
	})
	slices.Sort(found)
	return found, err
}

// where a source file lives: under pkg/, or under services/<name>/.
type location struct {
	kind    string // "pkg", "service", or "" for anything else (cmd/, root tests, ...)
	service string
}

func owner(rel string) location {
	switch {
	case strings.HasPrefix(rel, "pkg/"):
		return location{kind: "pkg"}
	case strings.HasPrefix(rel, "services/"):
		name, _, _ := strings.Cut(strings.TrimPrefix(rel, "services/"), "/")
		return location{kind: "service", service: name}
	}
	return location{}
}

func check(from location, imported string) string {
	servicesPrefix := modulePath + "/services/"
	if !strings.HasPrefix(imported, servicesPrefix) {
		return ""
	}
	target, _, _ := strings.Cut(strings.TrimPrefix(imported, servicesPrefix), "/")
	switch from.kind {
	case "pkg":
		return "pkg must not depend on any service"
	case "service":
		if target != from.service {
			return fmt.Sprintf("service %q must not depend on service %q: share data only through pkg/contracts", from.service, target)
		}
	}
	return ""
}

func TestServicesStayIndependent(t *testing.T) {
	got, err := violations(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		t.Error(v)
	}
}

// The rule must actually bite: build a tiny module tree that breaks it every way and expect each break found.
func TestBoundaryCheckDetectsViolations(t *testing.T) {
	root := t.TempDir()
	write := func(rel, imports string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package p\n\nimport (\n" + imports + ")\n"
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imp := func(paths ...string) string {
		var b strings.Builder
		for _, p := range paths {
			b.WriteString("\t_ \"" + modulePath + "/" + p + "\"\n")
		}
		return b.String()
	}

	// allowed
	write("services/console/internal/plans/plans.go", imp("pkg/contracts", "pkg/platform", "services/console/internal/wallet"))
	write("services/console/cmd/console-api/main.go", imp("services/console/internal/plans", "pkg/eventbus"))
	write("services/optimizer/internal/quote/quote.go", imp("pkg/engine/model"))
	write("pkg/eventbus/bus.go", imp("pkg/contracts", "pkg/platform"))
	write("cmd/topics/main.go", imp("pkg/contracts"))
	write("services/console/internal/plans/testdata/ignored.go", imp("services/driver/internal/tasks"))

	// forbidden
	write("services/console/internal/plans/bad.go", imp("services/backoffice/internal/fleet"))
	write("services/driver/internal/tasks/bad_test.go", imp("services/console/internal/wallet")) // tests count too
	write("services/backoffice/cmd/backoffice-api/main.go", imp("services/optimizer/internal/quote"))
	write("pkg/platform/bad.go", imp("services/console/internal/auth"))
	write("pkg/contracts/bad.go", imp("services/driver/internal/tasks", "pkg/platform"))

	got, err := violations(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pkg/contracts/bad.go imports " + modulePath + "/services/driver/internal/tasks: pkg must not depend on any service",
		"pkg/platform/bad.go imports " + modulePath + "/services/console/internal/auth: pkg must not depend on any service",
		"services/backoffice/cmd/backoffice-api/main.go imports " + modulePath + "/services/optimizer/internal/quote: service \"backoffice\" must not depend on service \"optimizer\": share data only through pkg/contracts",
		"services/console/internal/plans/bad.go imports " + modulePath + "/services/backoffice/internal/fleet: service \"console\" must not depend on service \"backoffice\": share data only through pkg/contracts",
		"services/driver/internal/tasks/bad_test.go imports " + modulePath + "/services/console/internal/wallet: service \"driver\" must not depend on service \"console\": share data only through pkg/contracts",
	}
	if !slices.Equal(got, want) {
		t.Errorf("violations =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
