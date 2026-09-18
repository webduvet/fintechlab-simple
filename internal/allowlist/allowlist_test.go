package allowlist

import "testing"

func TestAllowsComposeHostnameAndPort(t *testing.T) {
	l, err := Parse("receiver,receiver:8443,localhost,127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{
		"https://receiver:8443/webhooks",
		"https://receiver/webhooks",
		"http://localhost:8080/hook",
		"http://127.0.0.1:9/x",
	} {
		if err := l.Allowed(u); err != nil {
			t.Fatalf("%s: %v", u, err)
		}
	}
}

func TestRejectsOffListAndBadSchemes(t *testing.T) {
	l, err := Parse("receiver,127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{
		"https://evil.example/webhook",
		"https://169.254.169.254/latest",
		"file:///etc/passwd",
		"https://user:pass@receiver/hook",
		"not-a-url",
		"",
	} {
		if err := l.Allowed(u); err == nil {
			t.Fatalf("expected reject: %q", u)
		}
	}
}

func TestCIDR(t *testing.T) {
	l, err := Parse("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Allowed("http://10.1.2.3:8080/h"); err != nil {
		t.Fatal(err)
	}
	if err := l.Allowed("http://11.0.0.1/h"); err == nil {
		t.Fatal("expected cidr miss")
	}
}

func TestEmptyDeniesAll(t *testing.T) {
	l, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Allowed("https://receiver/webhooks"); err == nil {
		t.Fatal("empty list must deny")
	}
}
