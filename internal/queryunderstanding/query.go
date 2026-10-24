// Package queryunderstanding creates bounded, deterministic retrieval plans.
// It has no access to documents, identities, source IDs, SQL or a model.
package queryunderstanding

import (
	"regexp"
	"strings"
	"unicode"
)

type Intent uint8

const (
	Literal Intent = iota
	Duties
)

// Plan always starts with the original query. A focused variant removes only
// a recognized question frame, retaining the entire subject and its qualifiers.
// Adding new intents must not turn arbitrary questions into broad keyword lists.
type Plan struct {
	Intent   Intent
	Variants []string
}

var dutyFrames = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^какие (?:обязанности|функции|задачи) (?:выполняет|выполняют|возложены на) (.+)$`),
	regexp.MustCompile(`(?i)^(?:какие|каковы) (?:обязанности|функции|задачи) (?:у )?(.+)$`),
	regexp.MustCompile(`(?i)^за что (?:отвечает|отвечают) (.+)$`),
	regexp.MustCompile(`(?i)^что (?:должен|должна|должны|должно) (?:делать|выполнять) (.+)$`),
	regexp.MustCompile(`(?i)^что (.+) (?:должен|должна|должны) делать[?.!]*$`),
	regexp.MustCompile(`(?i)^чем (?:занимается|занимаются) (.+)$`),
	regexp.MustCompile(`(?i)^что входит в обязанности (.+)$`),
	regexp.MustCompile(`(?i)^в ч[её]м (?:состоят|заключаются) (?:обязанности|функции|задачи) (?:у )?(.+)$`),
}

// Understand is intentionally conservative. Explicit search syntax, negation,
// quoted text and unrecognized intents retain the existing FTS semantics.
// There are no rules for particular roles or document names.
func Understand(query string) Plan {
	plan := Plan{Variants: []string{query}}
	for _, r := range query {
		if !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsDigit(r) &&
			!unicode.IsSpace(r) && !strings.ContainsRune("?.,", r) {
			return plan
		}
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) }) {
		switch word {
		case "or", "не", "без", "кроме", "исключая", "или":
			return plan
		}
	}
	text := strings.Join(strings.Fields(query), " ")
	for _, frame := range dutyFrames {
		match := frame.FindStringSubmatch(text)
		if len(match) != 2 {
			continue
		}
		focus := strings.TrimSpace(strings.TrimRight(match[1], "?. "))
		if hasSubject(focus) && !strings.EqualFold(focus, text) {
			plan.Intent = Duties
			plan.Variants = append(plan.Variants, focus)
		}
		return plan
	}
	return plan
}

func hasSubject(text string) bool {
	for _, word := range strings.Fields(text) {
		switch strings.ToLower(strings.Trim(word, "?.,")) {
		case "у", "в", "на", "о", "об", "для", "и", "за", "что", "кто":
			continue
		}
		if strings.ContainsFunc(word, unicode.IsLetter) {
			return true
		}
	}
	return false
}
