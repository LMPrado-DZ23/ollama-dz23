package multillm

import "testing"

func TestBuiltInCLICatalogIsCompleteAndUnique(t *testing.T) {
	catalog := BuiltInCLICatalog()
	if len(catalog) != 46 {
		t.Fatalf("catalog has %d entries, want 46", len(catalog))
	}
	seen := make(map[string]bool)
	counts := make(map[string]int)
	for _, item := range catalog {
		if item.ID == "" || item.Name == "" || item.Section == "" || item.Mode == "" {
			t.Fatalf("incomplete catalog entry: %#v", item)
		}
		if seen[item.ID] {
			t.Fatalf("duplicate catalog id %q", item.ID)
		}
		seen[item.ID] = true
		counts[item.Section]++
	}
	if counts["code"] != 26 || counts["agent"] != 10 || counts["external"] != 10 {
		t.Fatalf("section counts = %#v", counts)
	}
}
