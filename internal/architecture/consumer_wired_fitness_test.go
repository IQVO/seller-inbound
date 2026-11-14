package architecture

// Consumer-wired sensor (exec-gaps 2026-11, seller-inbound).
//
//   - TestEveryConsumerConstructorIsStartedFromCmdApi: every exported
//     New*Consumer constructor declared in a non-test file of
//     internal/adapters/inbound/kafka must be CALLED from a non-test file of
//     cmd/api. The escape: NewSourcingConsumer, its handlers and use cases
//     existed (ADR 0012) but startBackground never called it, so
//     demand_heat_lines was never fed and nothing failed. Detection is by the
//     call `<alias of the kafka adapter import>.NewXConsumer(...)`; a bare
//     reference that is never called does not count.
//   - A vacuity guard fails when the scan finds zero constructors or zero
//     calls, so a renamed package or pattern cannot turn the sensor green.
//   - TestConsumerWiredSensorBites proves the detector flags a constructor
//     that cmd/api never calls and passes a wired one.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const cswireAdapterImport = "internal/adapters/inbound/kafka"

var cswirePattern = regexp.MustCompile(`^New[A-Za-z0-9]*Consumer$`)

// cswireDeclared returns the exported New*Consumer functions declared in files.
func cswireDeclared(files []*ast.File) []string {
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && cswirePattern.MatchString(fd.Name.Name) {
				out = append(out, fd.Name.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// cswireAlias returns the local name under which f imports the kafka adapter.
func cswireAlias(f *ast.File) string {
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasSuffix(path, cswireAdapterImport) {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "kafka"
	}
	return ""
}

// cswireCalled returns the set of adapter constructors called in files.
func cswireCalled(files []*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		alias := cswireAlias(f)
		if alias == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !cswirePattern.MatchString(sel.Sel.Name) {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == alias {
				out[sel.Sel.Name] = true
			}
			return true
		})
	}
	return out
}

// cswireUnwired returns the declared constructors that are never called.
func cswireUnwired(declared []string, called map[string]bool) []string {
	var out []string
	for _, name := range declared {
		if !called[name] {
			out = append(out, name)
		}
	}
	return out
}

func cswireViolation(name string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (contents rule)\n"+
		"  RULE:    every consumer constructor in internal/adapters/inbound/kafka is started from cmd/api\n"+
		"  WHAT:    %s is declared but cmd/api never calls it\n"+
		"  WHY:     a consumer nothing starts never reads its topic and nothing fails (ADR 0012: NewSourcingConsumer was dead code, so demand_heat_lines was never fed)\n"+
		"  FIX:     start it in cmd/api/wire_background.go startBackground with a mandatory <NAME>_CONSUMER_GROUP variable (ADR 0003), or delete the constructor. Do NOT loosen this test.\n",
		name)
}

// cswireParseDir parses the non-test .go files directly in dir.
func cswireParseDir(t *testing.T, dir string) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var out []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		out = append(out, f)
	}
	return out
}

func TestEveryConsumerConstructorIsStartedFromCmdApi(t *testing.T) {
	declared := cswireDeclared(cswireParseDir(t, filepath.Join("..", "adapters", "inbound", "kafka")))
	if len(declared) == 0 {
		t.Fatal("no New*Consumer constructor found under internal/adapters/inbound/kafka: the sensor's pattern no longer matches")
	}
	called := cswireCalled(cswireParseDir(t, filepath.Join("..", "..", "cmd", "api")))
	if len(called) == 0 {
		t.Fatal("cmd/api calls no New*Consumer constructor: the sensor's import alias scan no longer matches")
	}
	for _, name := range cswireUnwired(declared, called) {
		t.Errorf("%s", cswireViolation(name))
	}
}

const cswireAdapterSrc = `package kafka
func NewNodeConsumer() *Consumer { return nil }
func NewSourcingConsumer() *Consumer { return nil }
func (c *Consumer) NewNotAConstructorConsumer() {}
func newPrivateConsumer() *Consumer { return nil }`

const cswireWiredSrc = `package main
import inboundkafka "github.com/claudioed/seller-inbound/internal/adapters/inbound/kafka"
func start() {
	_ = inboundkafka.NewNodeConsumer()
	_ = inboundkafka.NewSourcingConsumer()
}`

// cswireBare references the constructor without calling it, and calls a
// same-named function of another package.
const cswireBare = `package main
import (
	inboundkafka "github.com/claudioed/seller-inbound/internal/adapters/inbound/kafka"
	other "example.com/other"
)
func start() {
	_ = inboundkafka.NewNodeConsumer()
	_ = inboundkafka.NewSourcingConsumer
	_ = other.NewSourcingConsumer()
}`

func cswireFile(t *testing.T, src string) []*ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "src.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return []*ast.File{f}
}

func TestConsumerWiredSensorBites(t *testing.T) {
	declared := cswireDeclared(cswireFile(t, cswireAdapterSrc))
	if got := strings.Join(declared, ","); got != "NewNodeConsumer,NewSourcingConsumer" {
		t.Fatalf("declared = %s, want NewNodeConsumer,NewSourcingConsumer (methods and unexported names are not constructors)", got)
	}
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"both wired passes", cswireWiredSrc, ""},
		{"a referenced but never called constructor is flagged, a foreign package's call does not count", cswireBare, "NewSourcingConsumer"},
		{"no call at all flags every constructor", "package main\nfunc start() {}", "NewNodeConsumer,NewSourcingConsumer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(cswireUnwired(declared, cswireCalled(cswireFile(t, tc.src))), ",")
			if got != tc.want {
				t.Fatalf("unwired = %q, want %q", got, tc.want)
			}
		})
	}
}
