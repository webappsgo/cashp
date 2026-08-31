package service

import (
	"runtime"
	"strings"
	"testing"
)

// takenProbe builds an idProbe backed by fixed sets, so ID selection is
// tested without reading /etc/passwd or /etc/group.
func takenProbe(uids, gids []int) idProbe {
	uidSet := make(map[int]bool, len(uids))
	for _, id := range uids {
		uidSet[id] = true
	}
	gidSet := make(map[int]bool, len(gids))
	for _, id := range gids {
		gidSet[id] = true
	}
	return idProbe{
		uidTaken: func(id int) bool { return uidSet[id] },
		gidTaken: func(id int) bool { return gidSet[id] },
	}
}

func TestFindAvailableIDInRangeScansDownwards(t *testing.T) {
	id, err := findAvailableIDInRange(200, 899, takenProbe(nil, nil))
	if err != nil {
		t.Fatalf("find id: %v", err)
	}
	if id != 899 {
		t.Errorf("id = %d, want the top of the range (899)", id)
	}
}

func TestFindAvailableIDInRangeSkipsTakenUIDsAndGIDs(t *testing.T) {
	// 899 is taken as a UID and 898 as a GID, so the first ID free on both
	// sides is 897 — the account requires UID == GID.
	id, err := findAvailableIDInRange(200, 899, takenProbe([]int{899}, []int{898}))
	if err != nil {
		t.Fatalf("find id: %v", err)
	}
	if id != 897 {
		t.Errorf("id = %d, want 897", id)
	}
}

func TestFindAvailableIDInRangeSkipsReservedIDs(t *testing.T) {
	// 101-110 are all reserved for well-known services, so a 100-110 range
	// can only yield 100.
	id, err := findAvailableIDInRange(100, 110, takenProbe(nil, nil))
	if err != nil {
		t.Fatalf("find id: %v", err)
	}
	if id != 100 {
		t.Errorf("id = %d, want 100: reserved IDs 101-110 must be skipped", id)
	}
}

func TestFindAvailableIDInRangeRejectsFullyReservedRange(t *testing.T) {
	if _, err := findAvailableIDInRange(996, 999, takenProbe(nil, nil)); err == nil {
		t.Fatal("expected an error for a range containing only reserved IDs")
	}
}

func TestFindAvailableIDInRangeExhausted(t *testing.T) {
	all := make([]int, 0, 6)
	for id := 200; id <= 205; id++ {
		all = append(all, id)
	}
	_, err := findAvailableIDInRange(200, 205, takenProbe(all, all))
	if err == nil {
		t.Fatal("expected an error when every ID in the range is taken")
	}
	if !strings.Contains(err.Error(), "no available UID/GID in safe range 200-205") {
		t.Errorf("error = %q, want the safe-range exhaustion message", err)
	}
}

func TestFindAvailableIDInRangeInvalidBounds(t *testing.T) {
	_, err := findAvailableIDInRange(899, 200, takenProbe(nil, nil))
	if err == nil {
		t.Fatal("expected an error for an inverted range")
	}
	if !strings.Contains(err.Error(), "invalid UID/GID range 899-200") {
		t.Errorf("error = %q, want the invalid-range message", err)
	}
}

func TestIDRangeMatchesPlatformBounds(t *testing.T) {
	low, high := idRange()
	wantLow, wantHigh := linuxIDRangeMin, linuxIDRangeMax
	if runtime.GOOS == "darwin" {
		wantLow, wantHigh = macIDRangeMin, macIDRangeMax
	}
	if low != wantLow || high != wantHigh {
		t.Errorf("idRange() = %d-%d, want %d-%d", low, high, wantLow, wantHigh)
	}
}

func TestReservedIDsCoverKnownServiceRanges(t *testing.T) {
	for _, id := range []int{65534, 999, 990, 980, 101, 110, 170, 179} {
		if !reservedIDs[id] {
			t.Errorf("ID %d must be reserved", id)
		}
	}
	for _, id := range []int{200, 500, 899} {
		if reservedIDs[id] {
			t.Errorf("ID %d must not be reserved: it is inside the safe range", id)
		}
	}
}
