package validate

import "testing"

// The exact shape that reached a real server: printf read %k in the password as a format
// verb and truncated the string, so what arrived had no host at all. url.Parse calls that
// "invalid port", which sent the operator looking at ports.
func TestMongoURIRejectsAShellMangledString(t *testing.T) {
	for _, tc := range []struct{ name, uri, want string }{
		{"truncated by printf at a percent", "mongodb://admin:5Jcmv!G2PLioUij", "no host in it"},
		{"scheme only", "mongodb://", "no host"},
		{"not a mongo uri", "postgres://x@y/z", "does not look like"},
		{"empty", "", "empty"},
		{"srv with a port", "mongodb+srv://a:b@c.example.net:27017/", "must not name a port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := MongoURI(tc.uri)
			if err == nil {
				t.Fatalf("MongoURI(%q) was accepted", tc.uri)
			}
			if !contains(err.Error(), tc.want) {
				t.Errorf("MongoURI(%q) said %q, want it to mention %q", tc.uri, err.Error(), tc.want)
			}
		})
	}
}

func TestMongoURIAcceptsTheRealForms(t *testing.T) {
	for _, uri := range []string{
		"mongodb://admin:pass@127.0.0.1:27017/?authSource=admin",
		"mongodb://admin:p%40ss@db.internal:27017/?tls=true&authSource=admin",
		"mongodb+srv://admin:pass@cluster0.ab12c.mongodb.net/?retryWrites=true",
		"mongodb://127.0.0.1:27017",
	} {
		if err := MongoURI(uri); err != nil {
			t.Errorf("MongoURI(%q) = %v, want accepted", uri, err)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
