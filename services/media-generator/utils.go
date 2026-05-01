package main

import (
	"fmt"
	"strings"
	"time"
)

// ─────────────────────────────────────────
// Helper
// ─────────────────────────────────────────

func formatRupiah(amount int64) string {
	s := fmt.Sprintf("%d", abs(amount))
	result := ""
	for i, c := range reverseStr(s) {
		if i > 0 && i%3 == 0 {
			result = "." + result
		}
		result = string(c) + result
	}
	return result
}

func reverseStr(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func formatDate(dateStr string) string {
	// Jika sudah ada spasi, kemungkinan sudah diformat di scraper (e.g. "04 April 2026")
	if strings.Contains(dateStr, " ") {
		return dateStr
	}

	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}

	monthNames := map[time.Month]string{
		time.January:   "Jan",
		time.February:  "Feb",
		time.March:     "Mar",
		time.April:     "Apr",
		time.May:       "Mei",
		time.June:      "Jun",
		time.July:      "Jul",
		time.August:    "Agt",
		time.September: "Sep",
		time.October:   "Okt",
		time.November:  "Nov",
		time.December:  "Des",
	}

	return fmt.Sprintf("%02d %s %d", t.Day(), monthNames[t.Month()], t.Year())
}
