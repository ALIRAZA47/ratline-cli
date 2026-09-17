package system

import (
	"encoding/binary"
	"io/fs"
	"sort"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// POSIX access control lists, as the kernel stores them in the system.posix_acl_access
// and system.posix_acl_default extended attributes.
//
// ratline reaches for an ACL in exactly one situation: a directory that root owns and
// systemd keeps root-owned, whose contents one tenant must nonetheless be able to read.
// A site's journal namespace is the case. systemd creates
// /var/log/journal/<machine-id>.<slug> for the namespace's journald instance and, on
// every start, puts the whole tree back to root:root if its owner ever differs — so a
// chgrp to the tenant's group would be undone by the next restart, taking every file
// with it. chown does not touch ACLs, and systemd leaves a tree alone whose owner
// already matches, so a named-group entry on the directory (as a default, inherited by
// each journal file journald creates) and on the files already there is a grant that
// survives.
//
// Written against the xattr format directly rather than by running setfacl, because
// the acl package is not on every minimal image and a root binary that shells out to
// change permissions is one more thing to audit. The encoding is small: a header with a
// version, then one fixed-size entry per (tag, permission, id).

// aclEntry is one entry of an ACL. Tag says what kind of principal it names; ID is a uid
// or gid for the named kinds and aclUndefinedID otherwise.
type aclEntry struct {
	Tag  uint16
	Perm uint16
	ID   uint32
}

const (
	aclVersion     = 0x0002
	aclUndefinedID = 0xFFFFFFFF

	// Tags, in the order the kernel insists they appear.
	aclUserObj  uint16 = 0x01 // the owner
	aclUser     uint16 = 0x02 // a named user
	aclGroupObj uint16 = 0x04 // the owning group
	aclGroup    uint16 = 0x08 // a named group
	aclMask     uint16 = 0x10 // the ceiling on every group-class entry
	aclOther    uint16 = 0x20 // everyone else

	aclRead    uint16 = 0x4
	aclWrite   uint16 = 0x2
	aclExecute uint16 = 0x1

	aclHeaderLen = 4
	aclEntryLen  = 8

	// XattrACLAccess and XattrACLDefault are the attribute names the kernel keeps the two
	// ACLs of a file under.
	XattrACLAccess  = "system.posix_acl_access"
	XattrACLDefault = "system.posix_acl_default"
)

// encodeACL lays entries out as the kernel expects them: version header, then entries
// sorted the way posix_acl_valid() requires (owner, named users, owning group, named
// groups, mask, other; named entries by id). A list that is out of order is refused by
// the kernel with EINVAL, so the sort is what makes a caller's order not matter.
func encodeACL(entries []aclEntry) []byte {
	sorted := append([]aclEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Tag != sorted[j].Tag {
			return sorted[i].Tag < sorted[j].Tag
		}
		return sorted[i].ID < sorted[j].ID
	})
	out := make([]byte, aclHeaderLen+aclEntryLen*len(sorted))
	binary.LittleEndian.PutUint32(out[0:], aclVersion)
	for i, e := range sorted {
		off := aclHeaderLen + i*aclEntryLen
		binary.LittleEndian.PutUint16(out[off:], e.Tag)
		binary.LittleEndian.PutUint16(out[off+2:], e.Perm)
		binary.LittleEndian.PutUint32(out[off+4:], e.ID)
	}
	return out
}

// decodeACL reads what encodeACL writes. An empty attribute is an empty list.
func decodeACL(b []byte) ([]aclEntry, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if len(b) < aclHeaderLen || (len(b)-aclHeaderLen)%aclEntryLen != 0 {
		return nil, rlerr.Genericf("malformed ACL: %d bytes", len(b))
	}
	if v := binary.LittleEndian.Uint32(b); v != aclVersion {
		return nil, rlerr.Genericf("unsupported ACL version %d", v)
	}
	n := (len(b) - aclHeaderLen) / aclEntryLen
	entries := make([]aclEntry, 0, n)
	for i := 0; i < n; i++ {
		off := aclHeaderLen + i*aclEntryLen
		entries = append(entries, aclEntry{
			Tag:  binary.LittleEndian.Uint16(b[off:]),
			Perm: binary.LittleEndian.Uint16(b[off+2:]),
			ID:   binary.LittleEndian.Uint32(b[off+4:]),
		})
	}
	return entries, nil
}

// groupReadACL is the ACL for something of the given permission bits that, in addition,
// lets one named group at it with perm.
//
// The owner, owning group and other entries restate the mode, so nothing about who
// could already read or write the file changes. The mask is the owning group's bits
// widened by perm — the mask caps every group-class entry, so without widening it the
// named group's entry would be recorded and then masked away, which `getfacl` shows as
// "#effective:---" and a tenant experiences as permission denied.
//
// Setting an access ACL rewrites the mode's group bits to the mask. For the 0640 journald
// writes that is a no-op; for a 0600 file it would show as 0640 while the owning group's
// real permission stayed at nothing — the ACL_GROUP_OBJ entry, not the mode, is what
// applies to it once an ACL is present.
func groupReadACL(mode fs.FileMode, gid int, perm uint16) []aclEntry {
	bits := uint16(mode.Perm())
	owner := (bits >> 6) & 7
	group := (bits >> 3) & 7
	other := bits & 7
	return []aclEntry{
		{Tag: aclUserObj, Perm: owner, ID: aclUndefinedID},
		{Tag: aclGroupObj, Perm: group, ID: aclUndefinedID},
		{Tag: aclGroup, Perm: perm, ID: uint32(gid)},
		{Tag: aclMask, Perm: group | perm, ID: aclUndefinedID},
		{Tag: aclOther, Perm: other, ID: aclUndefinedID},
	}
}

// aclGrantsGroup reports whether entries let gid read, with a mask that does not take the
// permission away again.
func aclGrantsGroup(entries []aclEntry, gid int, perm uint16) bool {
	mask := uint16(7)
	granted := false
	for _, e := range entries {
		switch e.Tag {
		case aclMask:
			mask = e.Perm
		case aclGroup:
			if e.ID == uint32(gid) && e.Perm&perm == perm {
				granted = true
			}
		}
	}
	return granted && mask&perm == perm
}
