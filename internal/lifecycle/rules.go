package lifecycle

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// Rule defines lifecycle expiration by key prefix and object age.
type Rule struct {
	Prefix      string
	ExpireAfter time.Duration
}

// ParseRules parses lifecycle rules from semicolon-separated env text.
//
// Format:
//
//	prefix=<key-prefix>,age=<duration>[;prefix=<key-prefix>,age=<duration>]
func ParseRules(raw string) ([]Rule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	ruleSpecs := strings.Split(raw, ";")
	rules := make([]Rule, 0, len(ruleSpecs))
	for i, spec := range ruleSpecs {
		rule, err := parseRule(spec)
		if err != nil {
			return nil, fmt.Errorf("parse lifecycle rule %d: %w", i+1, err)
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

func parseRule(spec string) (Rule, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Rule{}, fmt.Errorf("empty rule")
	}

	segments := strings.Split(spec, ",")
	values := map[string]string{}
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		parts := strings.SplitN(segment, "=", 2)
		if len(parts) != 2 {
			return Rule{}, fmt.Errorf("invalid segment %q", segment)
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "prefix", "age":
			values[key] = value
		default:
			return Rule{}, fmt.Errorf("unknown key %q", key)
		}
	}

	ageRaw, ok := values["age"]
	if !ok {
		return Rule{}, fmt.Errorf("missing age")
	}
	age, err := time.ParseDuration(ageRaw)
	if err != nil || age <= 0 {
		return Rule{}, fmt.Errorf("invalid age %q", ageRaw)
	}

	prefix, ok := values["prefix"]
	if !ok {
		return Rule{}, fmt.Errorf("missing prefix")
	}

	return Rule{Prefix: prefix, ExpireAfter: age}, nil
}

func matchesAnyRule(rules []Rule, key string, age time.Duration) bool {
	for _, rule := range rules {
		if age < rule.ExpireAfter {
			continue
		}
		if strings.HasPrefix(key, rule.Prefix) {
			return true
		}
	}
	return false
}

// ParseLifecycleXMLRules parses the S3 LifecycleConfiguration expiration subset.
func ParseLifecycleXMLRules(raw string) ([]Rule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var in struct {
		Rules []struct {
			Status string `xml:"Status"`
			Prefix string `xml:"Prefix"`
			Filter struct {
				Prefix string `xml:"Prefix"`
			} `xml:"Filter"`
			Expiration struct {
				Days int `xml:"Days"`
			} `xml:"Expiration"`
		} `xml:"Rule"`
	}
	if err := xml.NewDecoder(strings.NewReader(raw)).Decode(&in); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("parse lifecycle xml: %w", err)
	}
	rules := make([]Rule, 0, len(in.Rules))
	for i, rule := range in.Rules {
		if !strings.EqualFold(strings.TrimSpace(rule.Status), "Enabled") {
			continue
		}
		if rule.Expiration.Days <= 0 {
			return nil, fmt.Errorf("parse lifecycle xml rule %d: expiration days must be positive", i+1)
		}
		prefix := strings.TrimSpace(rule.Filter.Prefix)
		if prefix == "" {
			prefix = strings.TrimSpace(rule.Prefix)
		}
		rules = append(rules, Rule{Prefix: prefix, ExpireAfter: time.Duration(rule.Expiration.Days) * 24 * time.Hour})
	}
	return rules, nil
}
