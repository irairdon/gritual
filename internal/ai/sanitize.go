package ai

import (
	"errors"
	"math"
	"strings"
	"unicode"
)

var ErrBadName = errors.New("invalid food name")

func SanitizeFoodName(s string) (string, error) {
	s = stripC0(s)
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ErrBadName
	}
	runes := []rune(s)
	if len(runes) > 80 {
		s = strings.TrimSpace(string(runes[:80]))
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") {
		return "", ErrBadName
	}
	if strings.HasPrefix(lower, "ignore previous") || strings.HasPrefix(lower, "system:") {
		return "", ErrBadName
	}
	if s == "" {
		return "", ErrBadName
	}
	return s, nil
}

func SanitizeNotes(s string) string {
	s = stripC0(s)
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > 200 {
		s = strings.TrimSpace(string(runes[:200]))
	}
	return s
}

func validMacros(grams, kcal, protein, carbs, fat float64) bool {
	for _, n := range []float64{grams, kcal, protein, carbs, fat} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return false
		}
	}
	if grams > 5000 || kcal > 10000 {
		return false
	}
	return true
}

func stripC0(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}
