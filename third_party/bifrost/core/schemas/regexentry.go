package schemas

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var regexEntryCache sync.Map

const RegexEntryPrefix = "regex:"

func IsRegexEntry(entry string) bool {
	return strings.HasPrefix(entry, RegexEntryPrefix)
}

func compileRegexEntry(entry string) (*regexp.Regexp, error) {
	if cached, ok := regexEntryCache.Load(entry); ok {
		return cached.(*regexp.Regexp), nil
	}
	pattern := strings.TrimPrefix(entry, RegexEntryPrefix)
	if pattern == "" {
		return nil, fmt.Errorf("regex entry %q has no pattern", entry)
	}
	if pattern == "*" {
		return nil, fmt.Errorf("regex entry %q is not a pattern; use a bare \"*\" to allow or block everything", entry)
	}
	re, err := regexp.Compile("(?i)^(?:" + pattern + ")$")
	if err != nil {
		return nil, fmt.Errorf("regex entry %q does not compile: %v", entry, err)
	}
	regexEntryCache.Store(entry, re)
	return re, nil
}

func MatchEntry(entry, value string) bool {
	if !IsRegexEntry(entry) {
		return strings.EqualFold(entry, value)
	}
	re, err := compileRegexEntry(entry)
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func ValidateRegexEntries(entries []string) error {
	for _, entry := range entries {
		if !IsRegexEntry(entry) {
			continue
		}
		if _, err := compileRegexEntry(entry); err != nil {
			return err
		}
	}
	return nil
}
