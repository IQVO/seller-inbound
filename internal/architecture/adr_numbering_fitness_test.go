package architecture

// ADR numbering sensor (exec-gaps 2026-11, seller-inbound).
//
//   - TestADRNumbersAreUniqueAndIndexed: every docs/adr/NNNN-*.md carries a
//     number no other file carries, and docs/adr/index.md has exactly one row
//     linking each file. The escape: two sessions each took "the next free
//     number" (0015) in parallel branches; both landed on develop, the index
//     listed only one of them and nothing failed.
//   - A vacuity guard fails when the scan finds no ADR files or no index rows.
//   - TestADRNumberingSensorBites proves the detector flags a duplicate number
//     and an unindexed file from fixtures.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	adrFilePattern = regexp.MustCompile(`^(\d{4})-.+\.md$`)
	adrRowPattern  = regexp.MustCompile(`\]\(\./(\d{4}-[^)]+\.md)\)`)
)

// adrNumberingViolations returns one message per rule broken by files (the
// base names found in docs/adr) against the index text.
func adrNumberingViolations(files []string, index string) []string {
	var out []string
	byNumber := map[string][]string{}
	for _, f := range files {
		if m := adrFilePattern.FindStringSubmatch(f); m != nil {
			byNumber[m[1]] = append(byNumber[m[1]], f)
		}
	}
	numbers := make([]string, 0, len(byNumber))
	for n := range byNumber {
		numbers = append(numbers, n)
	}
	sort.Strings(numbers)
	for _, n := range numbers {
		if fs := byNumber[n]; len(fs) > 1 {
			sort.Strings(fs)
			out = append(out, fmt.Sprintf("ADR number %s is taken by %d files: %s", n, len(fs), strings.Join(fs, ", ")))
		}
	}
	rows := map[string]int{}
	for _, m := range adrRowPattern.FindAllStringSubmatch(index, -1) {
		rows[m[1]]++
	}
	for _, fs := range byNumber {
		for _, f := range fs {
			switch rows[f] {
			case 0:
				out = append(out, fmt.Sprintf("%s has no row in docs/adr/index.md", f))
			case 1:
			default:
				out = append(out, fmt.Sprintf("%s has %d rows in docs/adr/index.md", f, rows[f]))
			}
		}
	}
	sort.Strings(out)
	return out
}

func adrNumberingMessage(v string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (contents rule)\n"+
		"  RULE:    every ADR number is used by one file and every file has one row in docs/adr/index.md\n"+
		"  WHAT:    %s\n"+
		"  WHY:     parallel branches each took the next free number; both merged and the index listed one\n"+
		"  FIX:     renumber the later ADR (rename the file, its heading, every link and reference to it), add its index row. Do NOT loosen this test.\n", v)
}

func TestADRNumbersAreUniqueAndIndexed(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "adr")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		if adrFilePattern.MatchString(e.Name()) {
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		t.Fatal("no docs/adr/NNNN-*.md found: the sensor's pattern no longer matches")
	}
	index, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if len(adrRowPattern.FindAllString(string(index), -1)) == 0 {
		t.Fatal("docs/adr/index.md has no ADR rows: the sensor's row pattern no longer matches")
	}
	for _, v := range adrNumberingViolations(files, string(index)) {
		t.Error(adrNumberingMessage(v))
	}
}

func TestADRNumberingSensorBites(t *testing.T) {
	index := "| [0001](./0001-a.md) | A |\n| [0002](./0002-b.md) | B |\n"
	if v := adrNumberingViolations([]string{"0001-a.md", "0002-b.md"}, index); len(v) != 0 {
		t.Fatalf("a clean set was flagged: %v", v)
	}
	dup := adrNumberingViolations([]string{"0001-a.md", "0002-b.md", "0002-c.md"}, index)
	if len(dup) != 2 || !strings.Contains(strings.Join(dup, "|"), "ADR number 0002 is taken by 2 files") ||
		!strings.Contains(strings.Join(dup, "|"), "0002-c.md has no row") {
		t.Fatalf("a duplicate number with an unindexed file was not flagged precisely: %v", dup)
	}
	twice := adrNumberingViolations([]string{"0001-a.md"}, index+"| [0001](./0001-a.md) | again |\n")
	if len(twice) != 1 || !strings.Contains(twice[0], "has 2 rows") {
		t.Fatalf("a doubled index row was not flagged: %v", twice)
	}
}
