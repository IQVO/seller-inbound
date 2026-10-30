package architecture

// A2A inbound adapter fitness (docs/adr/0009-a2a-inbound-adapter.md).
//
//   - TestA2AAdapterDependencyRule: the adapter depends only on the
//     application and domain layers, and nothing in internal/ depends on it
//     (cmd/a2a wires it and nothing else touches it).
//   - TestA2AAgentIsReadOnlyAndHasNoOutboundClient: an ALLOWLIST sensor over
//     the adapter and cmd/a2a sources. The `Reads` seam holds exactly three
//     read methods, the only use-case/port/postgres identifiers the sources
//     may reference are the read ones, and no outbound adapter (Kafka, outbox,
//     SSCC issuer, ids, memory), no REST/MCP adapter and no A2A client is
//     imported. Allowlist, not denylist: a new write use case is refused
//     without having to be named here first.
//   - TestA2AReadOnlySensorBites proves each rule fires on a code fixture, so
//     the sensors cannot pass vacuously.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

const (
	a2aAdapterDir = "../adapters/inbound/a2a"
	a2aCmdDir     = "../../cmd/a2a"
)

func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})
}

// a2aReadMethods is the complete method set of the adapter's Reads seam: three
// of the four read use cases the REST and MCP adapters share.
var a2aReadMethods = []string{"GetInboundPlan", "GetInboundShipment", "GetSellerScorecard"}

// a2aAllowedSelectors maps a package qualifier to the identifiers the A2A
// sources may reference through it. Anything else (a write use case, a
// transactional seam, a publisher) is a violation.
var a2aAllowedSelectors = map[string][]string{
	"ports":    {"ErrNotFound"},
	"usecases": {"New", "Deps", "Config"},
	"postgres": {"RunMigrations", "NewPool", "NewSellerRepo", "NewPlanRepo", "NewShipmentRepo", "NewScorecardFacts"},
}

// a2aForbiddenImports are import-path fragments no A2A source may use, and why.
var a2aForbiddenImports = map[string]string{
	"a2a-go/v2/a2aclient":                "an outbound A2A client (the agent only answers; ADR 0009)",
	"/internal/adapters/outbound/kafka":  "Kafka: a read-only agent publishes no fact and consumes none",
	"/internal/adapters/outbound/outbox": "the outbox: a read-only agent never appends an event",
	"/internal/adapters/outbound/sscc":   "the SSCC issuer: only the write use cases issue SSCCs",
	"/internal/adapters/outbound/ids":    "the id generator: only the write use cases mint ids",
	"/internal/adapters/outbound/memory": "the in-memory test doubles (real Postgres only at runtime)",
	"/internal/adapters/inbound/http":    "the REST adapter (the adapters are siblings)",
	"/internal/adapters/inbound/mcp":     "the MCP adapter (the adapters are siblings)",
	"/internal/adapters/inbound/kafka":   "the Kafka consumers",
}

// a2aSourceViolations parses one non-test Go source and returns every
// forbidden import, every non-allowlisted selector on a guarded package and,
// when it declares the Reads interface, a method-set mismatch.
func a2aSourceViolations(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	out = append(out, a2aImportViolations(f)...)
	out = append(out, a2aSelectorViolations(f)...)
	out = append(out, a2aReadsViolations(f)...)
	return out, nil
}

func a2aImportViolations(f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		for fragment, why := range a2aForbiddenImports {
			if strings.Contains(path, fragment) {
				out = append(out, "imports "+path+": "+why)
			}
		}
	}
	return out
}

func a2aSelectorViolations(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		allowed, guarded := a2aAllowedSelectors[pkg.Name]
		if guarded && !contains(allowed, sel.Sel.Name) {
			out = append(out, "references "+pkg.Name+"."+sel.Sel.Name+", which is not on the read-only allowlist")
		}
		return true
	})
	return out
}

// a2aReadsViolations pins the method set of a declared `Reads` interface.
func a2aReadsViolations(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Reads" {
			return true
		}
		iface, ok := spec.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		var got []string
		for _, m := range iface.Methods.List {
			for _, name := range m.Names {
				got = append(got, name.Name)
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(a2aReadMethods, ",") {
			out = append(out, "Reads declares "+strings.Join(got, ",")+", want exactly "+strings.Join(a2aReadMethods, ","))
		}
		return false
	})
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestA2AAgentIsReadOnlyAndHasNoOutboundClient scans the adapter and the
// composition root (test files excluded: the wire tests legitimately drive the
// agent with the SDK client and seed it through the write use cases).
func TestA2AAgentIsReadOnlyAndHasNoOutboundClient(t *testing.T) {
	for _, dir := range []string{a2aAdapterDir, a2aCmdDir} {
		files := goFilesUnder(t, dir, false)
		if len(files) == 0 {
			t.Fatalf("no A2A sources found under %s; the sensor is looking in the wrong place", dir)
		}
		for _, path := range files {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			violations, err := a2aSourceViolations(path, src)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, v := range violations {
				t.Errorf("%s %s (docs/adr/0009-a2a-inbound-adapter.md)", path, v)
			}
		}
	}
}

// TestA2AReadOnlySensorBites proves the detector flags each forbidden shape
// on CODE (a comment that merely names a write seam must not count), and
// passes the real read wiring.
func TestA2AReadOnlySensorBites(t *testing.T) {
	bad := map[string]string{
		"a write use case":        "package x\nvar _ = usecases.CreateInboundPlan\n",
		"the unit of work":        "package x\nvar _ = postgres.NewUnitOfWork\n",
		"the outbox repo":         "package x\nvar _ = postgres.NewOutboxRepo\n",
		"the event outbox":        "package x\nvar _ = postgres.NewEventOutbox\n",
		"a Kafka import":          "package x\nimport _ \"github.com/claudioed/seller-inbound/internal/adapters/outbound/kafka\"\n",
		"an outbox import":        "package x\nimport _ \"github.com/claudioed/seller-inbound/internal/adapters/outbound/outbox\"\n",
		"an A2A client import":    "package x\nimport _ \"github.com/a2aproject/a2a-go/v2/a2aclient\"\n",
		"a memory double":         "package x\nimport _ \"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory\"\n",
		"the MCP adapter":         "package x\nimport _ \"github.com/claudioed/seller-inbound/internal/adapters/inbound/mcp\"\n",
		"a write method on Reads": "package x\ntype Reads interface{ GetInboundPlan(); GetInboundShipment(); GetSellerScorecard(); ConfirmInboundPlan() }\n",
		"a missing read method":   "package x\ntype Reads interface{ GetInboundPlan() }\n",
		"a non-read port":         "package x\nvar _ = ports.UnitOfWork\n",
	}
	for name, src := range bad {
		got, err := a2aSourceViolations("fixture.go", src)
		if err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations=%v, err=%v)", name, got, err)
		}
	}

	good := "package x\n// usecases.CreateInboundPlan and ports.UnitOfWork are only named here\n" +
		"import _ \"github.com/a2aproject/a2a-go/v2/a2asrv\"\n" +
		"var _ = usecases.New\nvar _ = ports.ErrNotFound\nvar _ = postgres.NewPlanRepo\n" +
		"type Reads interface{ GetSellerScorecard(); GetInboundPlan(); GetInboundShipment() }\n"
	if got, err := a2aSourceViolations("fixture.go", good); err != nil || len(got) != 0 {
		t.Errorf("false positive on the read wiring: %v (err=%v)", got, err)
	}
}
