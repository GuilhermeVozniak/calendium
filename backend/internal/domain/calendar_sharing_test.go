package domain

import (
	"errors"
	"testing"
)

func TestParseCalendarPermission(t *testing.T) {
	for _, valid := range []string{"free_busy", "reader", "editor"} {
		p, err := ParseCalendarPermission(valid)
		if err != nil || string(p) != valid {
			t.Fatalf("ParseCalendarPermission(%q) = %v, %v", valid, p, err)
		}
	}
	for _, invalid := range []string{"", "owner", "admin", "READER"} {
		if _, err := ParseCalendarPermission(invalid); !errors.Is(err, ErrValidation) {
			t.Fatalf("ParseCalendarPermission(%q) err = %v, want ErrValidation", invalid, err)
		}
	}
}

func TestCalendarPermissionOrdering(t *testing.T) {
	if !PermissionEditor.AtLeast(PermissionReader) || !PermissionReader.AtLeast(PermissionFreeBusy) {
		t.Fatalf("editor ≥ reader ≥ free_busy ordering broken")
	}
	if PermissionFreeBusy.AtLeast(PermissionReader) || PermissionReader.AtLeast(PermissionEditor) {
		t.Fatalf("lower permission must not satisfy higher")
	}
	if !PermissionEditor.AtLeast(PermissionEditor) {
		t.Fatalf("AtLeast must be reflexive")
	}
	if !PermissionReader.MorePermissive(PermissionFreeBusy) || PermissionReader.MorePermissive(PermissionReader) {
		t.Fatalf("MorePermissive must be strict")
	}
	// Unknown values rank below everything (defensive default).
	if CalendarPermission("bogus").AtLeast(PermissionFreeBusy) {
		t.Fatalf("unknown permission must rank lowest")
	}
}
