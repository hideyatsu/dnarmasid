package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"dnarmasid/shared/models"
	"gorm.io/gorm"
)

// ─────────────────────────────────────────────────
// MarketCondition — 12 unique market states
// ─────────────────────────────────────────────────

type MarketCondition int

const (
	ConditionBullishStrong MarketCondition = iota + 1
	ConditionBullishModerate
	ConditionBearishStreak
	ConditionBearishLow
	ConditionHighSpreadVolatile
	ConditionHighSpreadCalm
	ConditionLowSpreadBullish
	ConditionLowSpreadBearish
	ConditionNearATH
	ConditionStableLong
)

// ─────────────────────────────────────────────────
// MarketAnalysis — hasil analisis komprehensif
// ─────────────────────────────────────────────────

type MarketAnalysis struct {
	Condition    MarketCondition
	Trend7d      string  // "up" | "down" | "sideways"
	Streak       int     // consecutive days same direction
	Volatility   float64 // std dev / mean
	Spread       int64   // harga_jual - harga_buyback
	SpreadPct    float64
	DeltaJual    int64
	DeltaBuyback int64
	NearHigh7d   bool
	NearLow7d    bool
	NearHigh30d  bool
	High7d       int64
	Low7d        int64
	HookTTS      string
	HookVisual   string
	VisualBadge  string
}

// ─────────────────────────────────────────────────
// Hook Templates (per condition)
// ─────────────────────────────────────────────────

var hookTemplates = map[MarketCondition]struct {
	TTS    string
	Visual string
	Badge  string
}{
	ConditionBullishStrong: {
		TTS:    "Emas meroket! 7 hari berturut-turut naik, spread tipis. Momentum kuat!",
		Visual: "📈 BULLISH RUN!",
		Badge:  "📈",
	},
	ConditionBullishModerate: {
		TTS:    "Emas naik lagi! Sudah amankan posisimu?",
		Visual: "📈 EMAS NAIK!",
		Badge:  "📈",
	},
	ConditionBearishStreak: {
		TTS:    "3 hari turun terus! Saat yang tepat untuk akumulasi?",
		Visual: "⬇️ AKUMULASI?",
		Badge:  "⬇️",
	},
	ConditionBearishLow: {
		TTS:    "Harga menyentuh titik terendah 7 hari. Waktunya serok?",
		Visual: "🎯 TERENDAH 7 HARI",
		Badge:  "🎯",
	},
	ConditionHighSpreadVolatile: {
		TTS:    "Spread melebar, volatilitas tinggi. Hold dulu, jangan FOMO!",
		Visual: "⚠️ HOLD",
		Badge:  "⚠️",
	},
	ConditionHighSpreadCalm: {
		TTS:    "Spread melebar tapi pasar tenang. Ada apa dengan Antam?",
		Visual: "🔍 SPREAD LEBAR",
		Badge:  "🔍",
	},
	ConditionLowSpreadBullish: {
		TTS:    "Spread tipis + tren naik. Kondisi ideal untuk trading!",
		Visual: "💎 GOLDEN MOMENT",
		Badge:  "💎",
	},
	ConditionLowSpreadBearish: {
		TTS:    "Spread tipis saat harga turun. Waspada, jangan buru-buru.",
		Visual: "⏳ WASPADA",
		Badge:  "⏳",
	},
	ConditionNearATH: {
		TTS:    "Harga mendekati level tertinggi 30 hari! Akankah tembus?",
		Visual: "🔥 NEAR ATH!",
		Badge:  "🔥",
	},
	ConditionStableLong: {
		TTS:    "5 hari stabil. Pasar tenang — persiapan pergerakan besar?",
		Visual: "💤 TENANG",
		Badge:  "💤",
	},
}

// ─────────────────────────────────────────────────
// AnalyzeMarket — analisis komprehensif dengan data historis
// ─────────────────────────────────────────────────

func AnalyzeMarket(event *models.GoldScrapedEvent, db *gorm.DB) (*MarketAnalysis, error) {
	analysis := &MarketAnalysis{}

	// Get current prices (1 gram)
	var currentJual, currentBuyback int64
	for _, p := range event.Prices {
		if p.Gram == 1.0 {
			currentJual = p.SellPrice
			currentBuyback = p.BuyPrice
			break
		}
	}
	if currentJual == 0 && len(event.Prices) > 0 {
		currentJual = event.Prices[0].SellPrice
		currentBuyback = event.Prices[0].BuyPrice
	}

	// Get yesterday's prices from DB
	var yesterdayDate time.Time
	if t, err := time.Parse("02 Jan 2006", event.Date); err == nil {
		yesterdayDate = t.AddDate(0, 0, -1)
	}

	var yesterdayPrice models.GoldPrice
	if err := db.Where("DATE(date) = ? AND gram = 1.0", yesterdayDate.Format("2006-01-02")).First(&yesterdayPrice).Error; err != nil {
		log.Printf("[video-short] ⚠️ Could not fetch yesterday price: %v", err)
		yesterdayPrice.SellPrice = currentJual - event.ChangeAmt
		yesterdayPrice.BuyPrice = currentBuyback - event.BuybackChangeAmt
	}

	// Derived metrics
	analysis.DeltaJual = currentJual - yesterdayPrice.SellPrice
	analysis.DeltaBuyback = currentBuyback - yesterdayPrice.BuyPrice
	analysis.Spread = currentJual - currentBuyback
	if currentJual > 0 {
		analysis.SpreadPct = (float64(analysis.Spread) / float64(currentJual)) * 100
	}

	// Historical data (7 days)
	var prices7d []models.GoldPrice
	db.Where("gram = 1.0").Order("date DESC").Limit(7).Find(&prices7d)

	var prices7dJual []int64
	for _, p := range prices7d {
		prices7dJual = append(prices7dJual, p.SellPrice)
	}
	if len(prices7dJual) > 0 && prices7dJual[0] != currentJual {
		prices7dJual = append([]int64{currentJual}, prices7dJual...)
	}

	// Compute trend
	analysis.Trend7d = computeTrend(prices7dJual)

	// Compute streak
	analysis.Streak = computeStreak(prices7dJual)

	// Compute volatility
	analysis.Volatility = computeVolatility(prices7dJual)

	// High/low 7d
	var high7d, low7d int64
	if len(prices7dJual) > 0 {
		high7d = prices7dJual[0]
		low7d = prices7dJual[0]
		for _, p := range prices7dJual {
			if p > high7d {
				high7d = p
			}
			if p < low7d {
				low7d = p
			}
		}
	}
	analysis.High7d = high7d
	analysis.Low7d = low7d
	analysis.NearHigh7d = high7d > 0 && (float64(currentJual)/float64(high7d)) > 0.98
	analysis.NearLow7d = low7d > 0 && (float64(currentJual)/float64(low7d)) < 1.02

	// 30-day high
	var max30d int64
	db.Model(&models.GoldPrice{}).
		Where("gram = 1.0 AND date >= ?", time.Now().AddDate(0, 0, -30)).
		Select("MAX(sell_price)").Scan(&max30d)
	analysis.NearHigh30d = max30d > 0 && (float64(currentJual)/float64(max30d)) > 0.99

	// Determine market condition (priority order)
	analysis.Condition = determineCondition(analysis)

	// Load hook template
	if hook, ok := hookTemplates[analysis.Condition]; ok {
		analysis.HookTTS = hook.TTS
		analysis.HookVisual = hook.Visual
		analysis.VisualBadge = hook.Badge
	} else {
		analysis.HookTTS = "Harga emas masih stabil. Cek update terbarunya."
		analysis.HookVisual = "⚖️ STABIL"
		analysis.VisualBadge = "⚖️"
	}

	log.Printf("[video-short] 📊 Analysis: condition=%d trend=%s streak=%d volatility=%.4f spread=%.2f%%",
		analysis.Condition, analysis.Trend7d, analysis.Streak, analysis.Volatility, analysis.SpreadPct)

	return analysis, nil
}

// ─────────────────────────────────────────────────
// determineCondition — priority-based condition matching
// ─────────────────────────────────────────────────

func determineCondition(a *MarketAnalysis) MarketCondition {
	// Priority 1: Near ATH
	if a.NearHigh30d {
		return ConditionNearATH
	}
	// Priority 2: Bullish Strong (up + spread < 6%)
	if a.DeltaJual > 10000 && a.DeltaBuyback > 10000 && a.SpreadPct < 6.0 && a.Trend7d == "up" {
		return ConditionBullishStrong
	}
	// Priority 3: Bullish Moderate
	if a.DeltaJual > 10000 && a.DeltaBuyback > 10000 {
		return ConditionBullishModerate
	}
	// Priority 4: Bearish Streak
	if a.DeltaJual < -10000 && a.DeltaBuyback < -10000 && a.Streak >= 3 {
		return ConditionBearishStreak
	}
	// Priority 5: Bearish Low
	if a.DeltaJual < -10000 && a.NearLow7d {
		return ConditionBearishLow
	}
	// Priority 6: High Spread Volatile
	if a.SpreadPct > 9.0 && a.Volatility > 0.01 {
		return ConditionHighSpreadVolatile
	}
	// Priority 7: High Spread Calm
	if a.SpreadPct > 9.0 {
		return ConditionHighSpreadCalm
	}
	// Priority 8: Low Spread Bullish
	if a.SpreadPct < 6.0 && a.Trend7d == "up" {
		return ConditionLowSpreadBullish
	}
	// Priority 9: Low Spread Bearish
	if a.SpreadPct < 6.0 && a.Trend7d == "down" {
		return ConditionLowSpreadBearish
	}
	// Priority 10: Stable Long
	if a.Streak >= 5 && a.Trend7d == "sideways" {
		return ConditionStableLong
	}
	// Default
	return ConditionStableLong
}

// ─────────────────────────────────────────────────
// Helper functions
// ─────────────────────────────────────────────────

func computeTrend(prices []int64) string {
	if len(prices) < 2 {
		return "sideways"
	}
	n := len(prices)
	sumX := 0
	sumY := int64(0)
	sumXY := int64(0)
	sumX2 := 0

	for i, p := range prices {
		sumX += i
		sumY += p
		sumXY += int64(i) * p
		sumX2 += i * i
	}

	numerator := int64(n)*sumXY - int64(sumX)*sumY
	denominator := int64(n)*int64(sumX2) - int64(sumX*sumX)
	if denominator == 0 {
		return "sideways"
	}

	slope := float64(numerator) / float64(denominator)
	mean := float64(sumY) / float64(n)
	normalizedSlope := slope / mean

	if normalizedSlope > 0.001 {
		return "up"
	} else if normalizedSlope < -0.001 {
		return "down"
	}
	return "sideways"
}

func computeStreak(prices []int64) int {
	if len(prices) < 2 {
		return 0
	}
	streak := 1
	lastDir := 0

	for i := 1; i < len(prices); i++ {
		diff := prices[i] - prices[i-1]
		var dir int
		if diff > 0 {
			dir = 1
		} else if diff < 0 {
			dir = -1
		}

		if dir == lastDir && dir != 0 {
			streak++
		} else {
			streak = 1
			lastDir = dir
		}
	}
	return streak
}

func computeVolatility(prices []int64) float64 {
	if len(prices) < 2 {
		return 0
	}
	sum := int64(0)
	for _, p := range prices {
		sum += p
	}
	mean := float64(sum) / float64(len(prices))

	variance := 0.0
	for _, p := range prices {
		diff := float64(p) - mean
		variance += diff * diff
	}
	variance /= float64(len(prices))

	return math.Sqrt(variance) / mean
}

func formatRupiah(amount int64) string {
	negative := amount < 0
	if negative {
		amount = -amount
	}
	str := fmt.Sprintf("%d", amount)
	var result strings.Builder
	for i, ch := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result.WriteRune('.')
		}
		result.WriteRune(ch)
	}
	if negative {
		return "-" + result.String()
	}
	return result.String()
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func ensureDir(path string) {
	os.MkdirAll(path, 0755)
}