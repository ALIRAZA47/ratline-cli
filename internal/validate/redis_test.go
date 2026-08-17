package validate

import "testing"

func TestRedisKeyspace(t *testing.T) {
	for _, ok := range []string{"shop", "shop_app", "tenant-42", "Analytics"} {
		if err := RedisKeyspace(ok); err != nil {
			t.Errorf("RedisKeyspace(%q) = %v", ok, err)
		}
	}
	// A space, a glob or a control byte could rewrite the ACL rule or the ~pattern.
	for _, bad := range []string{"", "shop *", "shop:*", "shop\nDEL", "a b", "shop\x00"} {
		if err := RedisKeyspace(bad); err == nil {
			t.Errorf("RedisKeyspace(%q) was accepted", bad)
		}
	}
}

func TestRedisUsername(t *testing.T) {
	if err := RedisUsername("shop_app"); err != nil {
		t.Errorf("shop_app: %v", err)
	}
	// "default" is Redis's own admin user; provisioning over it is refused.
	for _, bad := range []string{"", "default", "Default", "a b", "on >pass"} {
		if err := RedisUsername(bad); err == nil {
			t.Errorf("RedisUsername(%q) was accepted", bad)
		}
	}
}

func TestRedisRole(t *testing.T) {
	for _, ok := range []string{"read", "readWrite", "dbOwner"} {
		if err := RedisRole(ok); err != nil {
			t.Errorf("RedisRole(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "+@all", "admin"} {
		if err := RedisRole(bad); err == nil {
			t.Errorf("RedisRole(%q) was accepted", bad)
		}
	}
	if len(RedisRoles()) != 3 {
		t.Errorf("RedisRoles() = %d, want 3", len(RedisRoles()))
	}
}

func TestRedisURI(t *testing.T) {
	for _, ok := range []string{"redis://:pass@127.0.0.1:6379", "rediss://user:pass@host:6380"} {
		if err := RedisURI(ok); err != nil {
			t.Errorf("RedisURI(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "http://x", "mongodb://x", "redis://"} {
		if err := RedisURI(bad); err == nil {
			t.Errorf("RedisURI(%q) was accepted", bad)
		}
	}
}
