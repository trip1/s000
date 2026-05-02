package lifecycle

import (
	"strings"
	"testing"
	"time"
)

func TestParseRules(t *testing.T) {
	t.Parallel()

	rules, err := ParseRules("prefix=logs/,age=24h;prefix=tmp/,age=30m")
	if err != nil {
		t.Fatalf("parse rules failed: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if rules[0].Prefix != "logs/" || rules[0].ExpireAfter != 24*time.Hour {
		t.Fatalf("unexpected first rule: %+v", rules[0])
	}
	if rules[1].Prefix != "tmp/" || rules[1].ExpireAfter != 30*time.Minute {
		t.Fatalf("unexpected second rule: %+v", rules[1])
	}
}

func TestParseRulesInvalid(t *testing.T) {
	t.Parallel()

	_, err := ParseRules("prefix=logs/")
	if err == nil {
		t.Fatal("expected parse error for missing age")
	}
	if !strings.Contains(err.Error(), "missing age") {
		t.Fatalf("expected missing age parse error, got %v", err)
	}
}

func TestParseLifecycleXMLRules(t *testing.T) {
	t.Parallel()

	rules, err := ParseLifecycleXMLRules(`<LifecycleConfiguration><Rule><ID>logs</ID><Status>Enabled</Status><Filter><Prefix>logs/</Prefix></Filter><Expiration><Days>7</Days></Expiration></Rule><Rule><ID>off</ID><Status>Disabled</Status><Prefix>tmp/</Prefix><Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`)
	if err != nil {
		t.Fatalf("parse lifecycle xml failed: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected one enabled rule, got %d", len(rules))
	}
	if rules[0].Prefix != "logs/" || rules[0].ExpireAfter != 7*24*time.Hour {
		t.Fatalf("unexpected parsed rule: %+v", rules[0])
	}
}
