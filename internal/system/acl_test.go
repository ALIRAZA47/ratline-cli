package system

import (
	"encoding/binary"
	"testing"
)

// The layout the kernel reads back: a version-2 header, then eight bytes per entry, in
// the tag order posix_acl_valid() insists on. Anything else is EINVAL at setxattr time,
// which is a failure `site add` would report as "setting the ACL" with no further clue.
func TestACLEncodingIsWhatTheKernelExpects(t *testing.T) {
	b := encodeACL(groupReadACL(0o640, 1005, aclRead))
	if len(b) != aclHeaderLen+5*aclEntryLen {
		t.Fatalf("encoded %d bytes, want %d", len(b), aclHeaderLen+5*aclEntryLen)
	}
	if v := binary.LittleEndian.Uint32(b); v != aclVersion {
		t.Errorf("version = %d, want %d", v, aclVersion)
	}
	entries, err := decodeACL(b)
	if err != nil {
		t.Fatalf("decodeACL = %v", err)
	}
	want := []aclEntry{
		{aclUserObj, 6, aclUndefinedID},
		{aclGroupObj, 4, aclUndefinedID},
		{aclGroup, 4, 1005},
		{aclMask, 4, aclUndefinedID},
		{aclOther, 0, aclUndefinedID},
	}
	if len(entries) != len(want) {
		t.Fatalf("decoded %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

// A caller's order must not matter: the kernel's does.
func TestOutOfOrderEntriesAreSortedForTheKernel(t *testing.T) {
	entries, err := decodeACL(encodeACL([]aclEntry{
		{aclOther, 0, aclUndefinedID},
		{aclGroup, 4, 2000},
		{aclMask, 4, aclUndefinedID},
		{aclGroup, 4, 1000},
		{aclUserObj, 6, aclUndefinedID},
		{aclGroupObj, 4, aclUndefinedID},
	}))
	if err != nil {
		t.Fatalf("decodeACL = %v", err)
	}
	var lastTag uint16
	var lastID uint32
	for _, e := range entries {
		if e.Tag < lastTag || (e.Tag == lastTag && e.ID < lastID) {
			t.Fatalf("entries are not in kernel order: %+v", entries)
		}
		lastTag, lastID = e.Tag, e.ID
	}
}

// The mask caps every group-class entry. A grant that leaves the mask where a 0600 file
// puts it would be recorded and then masked to nothing — exactly what getfacl prints as
// "#effective:---" and what a tenant experiences as permission denied.
func TestTheGrantWidensTheMaskSoItIsEffective(t *testing.T) {
	entries := groupReadACL(0o600, 1005, aclRead)
	if !aclGrantsGroup(entries, 1005, aclRead) {
		t.Errorf("the group is not effectively granted read: %+v", entries)
	}
	if aclGrantsGroup(entries, 1006, aclRead) {
		t.Error("a group that was never named is granted read")
	}
	// The owner's and everyone else's bits are exactly the mode's.
	for _, e := range entries {
		switch e.Tag {
		case aclUserObj:
			if e.Perm != 6 {
				t.Errorf("owner perm = %d, want 6", e.Perm)
			}
		case aclOther:
			if e.Perm != 0 {
				t.Errorf("other perm = %d, want 0", e.Perm)
			}
		}
	}
	// And a mask that hides the entry is seen for what it is.
	hidden := []aclEntry{{aclGroup, aclRead, 1005}, {aclMask, 0, aclUndefinedID}}
	if aclGrantsGroup(hidden, 1005, aclRead) {
		t.Error("a masked-away grant was reported as effective")
	}
}

func TestDecodeRefusesWhatIsNotAnACL(t *testing.T) {
	if _, err := decodeACL([]byte{1, 2, 3}); err == nil {
		t.Error("three bytes decoded as an ACL")
	}
	bad := encodeACL(nil)
	binary.LittleEndian.PutUint32(bad, 7)
	if _, err := decodeACL(bad); err == nil {
		t.Error("an unknown version decoded")
	}
	if entries, err := decodeACL(nil); err != nil || len(entries) != 0 {
		t.Errorf("no attribute should be an empty list, got %v, %v", entries, err)
	}
}
