package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ── Trading Config ──────────────────────────────────────────────────────

type TradingExportConfig struct {
	VaultRoot  string
	OutputDir  string
	JournalDir string
	DryRun     bool
	Clean      bool
}

type DailyResult struct {
	Date   string
	PnL    float64
	Trades int
	Result string // win, loss, breakeven, flat
}

// ── CSV/JSON helpers ────────────────────────────────────────────────────

func loadCSV(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) { return nil, nil }
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil { return nil, nil }
	var rows []map[string]string
	for {
		record, err := r.Read()
		if err == io.EOF { break }
		if err != nil { continue }
		row := make(map[string]string)
		for i, h := range header {
			if i < len(record) { row[h] = record[i] }
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func loadJSON(path string) ([]map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) { return nil, nil }
		return nil, err
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal(data, &arr); err != nil {
		var single map[string]interface{}
		if err2 := json.Unmarshal(data, &single); err2 == nil {
			return []map[string]interface{}{single}, nil
		}
		return nil, nil
	}
	return arr, nil
}

func safeFloat(v string) float64 {
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

func fmtPnL(v float64) string {
	if v >= 0 { return fmt.Sprintf("+$%.2f", v) }
	return fmt.Sprintf("-$%.2f", math.Abs(v))
}

func fmtPct(v string) string {
	f := safeFloat(v)
	if f >= 0 { return fmt.Sprintf("+%.1f%%", f) }
	return fmt.Sprintf("%.1f%%", f)
}

func classifyOutcome(pnl float64) string {
	if pnl > 1 { return "win" }
	if pnl < -1 { return "loss" }
	return "breakeven"
}

var outcomeEmoji = map[string]string{"win": "🟢", "loss": "🔴", "breakeven": "🟡", "flat": "⚪"}
var dirEmoji = map[string]string{"long": "📈", "short": "📉"}

// ── Daily Note Writer ───────────────────────────────────────────────────

func writeTradingDaily(dateStr string, equityRow map[string]string, optionsRows []map[string]string,
	equityTrades []map[string]interface{}, optionsTrades []map[string]interface{},
	outDir string, dryRun bool) DailyResult {

	totalPnL := 0.0
	totalTrades := 0

	// Equity P&L
	if equityRow != nil {
		totalPnL += safeFloat(equityRow["daily_pnl"])
	}
	// Options P&L
	for _, r := range optionsRows {
		totalPnL += safeFloat(r["pnl"])
		totalTrades++
	}
	totalTrades += len(equityTrades)

	outcome := classifyOutcome(totalPnL)
	oEmoji := outcomeEmoji[outcome]

	// Parse day of week
	dayName := "?"
	if t, err := time.Parse("2006-01-02", dateStr); err == nil {
		dayName = t.Format("Mon")
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "date: %s\nday: %s\n", dateStr, dayName)
	fmt.Fprintf(&b, "pnl: %.2f\ntrades: %d\noutcome: %s\n", totalPnL, totalTrades, outcome)

	// Strategy tags
	strategies := make(map[string]bool)
	if equityRow != nil && equityRow["strategy"] != "" { strategies[equityRow["strategy"]] = true }
	for _, r := range optionsRows { if r["underlying"] != "" { strategies["Triple Crown"] = true } }
	if len(strategies) > 0 {
		ss := obsSortedKeys(strategies) // reuse helper from export_obsidian.go
		fmt.Fprintf(&b, "strategies: [%s]\n", strings.Join(ss, ", "))
	}
	fmt.Fprintf(&b, "tags: [\"#trading\", \"#trading/daily\"]\n---\n\n")
	fmt.Fprintf(&b, "# %s %s Trading Day — %s\n\n", oEmoji, dateStr, dayName)
	fmt.Fprintf(&b, "> **P&L: %s** · %d trades · ← [[Trading Journal]]\n\n", fmtPnL(totalPnL), totalTrades)

	// Equity section
	if equityRow != nil {
		b.WriteString("## 📊 Equity (ORB)\n\n")
		b.WriteString("| Metric | Value |\n|---|---|\n")
		fmt.Fprintf(&b, "| Equity | $%s |\n", equityRow["equity"])
		fmt.Fprintf(&b, "| Daily P&L | %s |\n", fmtPnL(safeFloat(equityRow["daily_pnl"])))
		fmt.Fprintf(&b, "| Signals | %s |\n", equityRow["signals"])
		fmt.Fprintf(&b, "| Win Rate | %s%% |\n", equityRow["win_pct"])
		b.WriteString("\n")
	}

	// Options scorecard
	if len(optionsRows) > 0 {
		b.WriteString("## 👑 Options (Triple Crown)\n\n")
		b.WriteString("| Symbol | Dir | Entry | Exit | P&L | P&L% | Exit | Hold |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|\n")
		for _, t := range optionsRows {
			pnl := safeFloat(t["pnl"])
			e := "🟢"; if pnl < 0 { e = "🔴" }
			de := dirEmoji[t["direction"]]; if de == "" { de = "" }
			fmt.Fprintf(&b, "| %s%s | %s | $%s | $%s | %s %s | %s | %s | %sm |\n",
				de, t["symbol"], t["direction"], t["entry"], t["exit"],
				e, fmtPnL(pnl), fmtPct(t["pnl_pct"]), t["exit_reason"], t["hold_min"])
		}
		b.WriteString("\n")
	}

	// Detailed trade JSON
	if len(optionsTrades) > 0 {
		b.WriteString("### Trade Details\n\n")
		for i, trade := range optionsTrades {
			dir, _ := trade["direction"].(string)
			und, _ := trade["underlying"].(string)
			strike, _ := trade["strike"].(float64)
			otype, _ := trade["type"].(string)
			pnl := 0.0
			if v, ok := trade["pnl"].(float64); ok { pnl = v }
			pe := "🟢"; if pnl < 0 { pe = "🔴" }
			de := dirEmoji[dir]

			fmt.Fprintf(&b, "#### %s Trade %d: [[%s]] $%.0f %s\n\n", de, i+1, und, strike, otype)
			if entry, ok := trade["fill_price"].(float64); ok {
				fmt.Fprintf(&b, "- **Entry:** $%.2f\n", entry)
			} else if entry, ok := trade["limit_price"].(float64); ok {
				fmt.Fprintf(&b, "- **Entry:** $%.2f\n", entry)
			}
			if exit, ok := trade["exit_price"].(float64); ok {
				fmt.Fprintf(&b, "- **Exit:** $%.2f\n", exit)
			}
			fmt.Fprintf(&b, "- **P&L:** %s %s\n", pe, fmtPnL(pnl))

			if d, ok := trade["delta"].(float64); ok {
				fmt.Fprintf(&b, "- **Greeks:** Δ=%.3f Γ=%.4f Θ=%.4f IV=%.2f\n",
					d, numOr(trade["gamma"], 0), numOr(trade["theta"], 0), numOr(trade["iv"], 0))
			}
			if rw, ok := trade["range_width"].(float64); ok {
				fmt.Fprintf(&b, "- **Range:** %.2f\n", rw)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("---\n")
	fmt.Fprintf(&b, "*Synced by `cortex export obsidian --trading` v1.3 · %s*\n", time.Now().Format("2006-01-02 15:04"))

	fn := dateStr + ".md"
	path := filepath.Join(outDir, fn)

	if dryRun {
		fmt.Printf("  📄 Would write: trading/%s (%d trades, %s)\n", fn, totalTrades, fmtPnL(totalPnL))
	} else {
		os.MkdirAll(outDir, 0755)
		os.WriteFile(path, []byte(b.String()), 0644)
		fmt.Printf("  ✅ trading/%s — %d trades, %s\n", fn, totalTrades, fmtPnL(totalPnL))
	}

	return DailyResult{dateStr, totalPnL, totalTrades, outcome}
}

func numOr(v interface{}, def float64) float64 {
	if f, ok := v.(float64); ok { return f }
	return def
}

// ── Trading Index Writer ────────────────────────────────────────────────

func writeTradingIndex(results []DailyResult, outDir string, dryRun bool) error {
	path := filepath.Join(outDir, "Trading Journal.md")

	totalPnL := 0.0
	totalTrades := 0
	wins := 0
	for _, r := range results {
		totalPnL += r.PnL
		totalTrades += r.Trades
		if r.Result == "win" { wins++ }
	}
	days := len(results)
	winRate := 0.0
	if days > 0 { winRate = float64(wins) / float64(days) * 100 }

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "synced: %s\ntotal_pnl: %.2f\ntrading_days: %d\ntotal_trades: %d\n",
		time.Now().Format("2006-01-02 15:04"), totalPnL, days, totalTrades)
	b.WriteString("tags: [\"#trading\", \"#trading/journal\", \"#MOC\"]\n---\n\n")
	b.WriteString("# 📈 Trading Journal\n\n")
	fmt.Fprintf(&b, "> **%d trading days** · **%d trades** · **P&L: %s** · Win days: %d/%d (%.0f%%)\n\n",
		days, totalTrades, fmtPnL(totalPnL), wins, days, winRate)
	b.WriteString("← [[Cortex Dashboard]] · [[Trading]] · [[Triple Crown]] · [[ORB]]\n\n")

	b.WriteString("## Performance\n\n| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Total P&L | %s |\n| Trading Days | %d |\n| Total Trades | %d |\n| Winning Days | %d (%.0f%%) |\n",
		fmtPnL(totalPnL), days, totalTrades, wins, winRate)
	losses := 0
	for _, r := range results { if r.Result == "loss" { losses++ } }
	fmt.Fprintf(&b, "| Losing Days | %d |\n", losses)
	if days > 0 { fmt.Fprintf(&b, "| Avg Daily P&L | %s |\n", fmtPnL(totalPnL/float64(days))) }
	b.WriteString("\n## Daily Log\n\n| Date | Day | P&L | Trades | Outcome |\n|---|---|---|---|---|\n")

	sorted := make([]DailyResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date > sorted[j].Date })
	for _, r := range sorted {
		day := "?"
		if t, err := time.Parse("2006-01-02", r.Date); err == nil { day = t.Format("Mon") }
		e := outcomeEmoji[r.Result]
		fmt.Fprintf(&b, "| [[%s]] | %s | %s %s | %d | %s |\n", r.Date, day, e, fmtPnL(r.PnL), r.Trades, r.Result)
	}

	b.WriteString("\n---\n")
	fmt.Fprintf(&b, "*Synced by `cortex export obsidian --trading` v1.3 · %s*\n", time.Now().Format("2006-01-02 15:04"))

	if dryRun {
		fmt.Printf("  📄 Would write: trading/Trading Journal.md (%d days)\n", days)
		return nil
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// ── Main Trading Export ─────────────────────────────────────────────────

func runExportTrading(args []string) error {
	cfg := TradingExportConfig{}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--vault" && i+1 < len(args): i++; cfg.VaultRoot = args[i]
		case strings.HasPrefix(args[i], "--vault="): cfg.VaultRoot = strings.TrimPrefix(args[i], "--vault=")
		case args[i] == "--journal" && i+1 < len(args): i++; cfg.JournalDir = args[i]
		case strings.HasPrefix(args[i], "--journal="): cfg.JournalDir = strings.TrimPrefix(args[i], "--journal=")
		case args[i] == "--dry-run" || args[i] == "-n": cfg.DryRun = true
		case args[i] == "--clean": cfg.Clean = true
		}
	}
	if cfg.VaultRoot == "" { cfg.VaultRoot, _ = os.Getwd() }
	if cfg.JournalDir == "" { cfg.JournalDir = filepath.Join(cfg.VaultRoot, "trading_journal") }
	cfg.OutputDir = filepath.Join(cfg.VaultRoot, "_cortex", "trading")

	fmt.Println("📈 Trading Journal → Obsidian Export")
	fmt.Printf("   Vault: %s\n   Journal: %s\n   Output: %s\n\n", cfg.VaultRoot, cfg.JournalDir, cfg.OutputDir)

	if cfg.Clean && !cfg.DryRun {
		os.RemoveAll(cfg.OutputDir)
		fmt.Println("  🗑️  Cleaned trading/ directory")
	}

	// Load equity scorecard
	fmt.Println("📊 Loading equity scorecard...")
	equityRows, _ := loadCSV(filepath.Join(cfg.JournalDir, "forward_test", "scorecard.csv"))
	equityByDate := make(map[string]map[string]string)
	for _, r := range equityRows { equityByDate[r["date"]] = r }
	fmt.Printf("   %d equity days\n", len(equityRows))

	// Load options scorecard
	fmt.Println("📊 Loading options scorecard...")
	optionsRows, _ := loadCSV(filepath.Join(cfg.JournalDir, "forward_test", "options", "scorecard.csv"))
	optionsByDate := make(map[string][]map[string]string)
	for _, r := range optionsRows { optionsByDate[r["date"]] = append(optionsByDate[r["date"]], r) }
	fmt.Printf("   %d options trades\n", len(optionsRows))

	// Load trade JSONs
	fmt.Println("📂 Loading trade details...")
	equityTradesByDate := make(map[string][]map[string]interface{})
	eTradeDir := filepath.Join(cfg.JournalDir, "forward_test", "trades")
	if entries, err := os.ReadDir(eTradeDir); err == nil {
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".json" {
				ds := strings.TrimSuffix(e.Name(), ".json")
				trades, _ := loadJSON(filepath.Join(eTradeDir, e.Name()))
				equityTradesByDate[ds] = trades
			}
		}
	}

	optionsTradesByDate := make(map[string][]map[string]interface{})
	oTradeDir := filepath.Join(cfg.JournalDir, "forward_test", "options", "trades")
	if entries, err := os.ReadDir(oTradeDir); err == nil {
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".json" {
				ds := strings.TrimSuffix(e.Name(), ".json")
				ds = strings.TrimPrefix(ds, "trades_")
				trades, _ := loadJSON(filepath.Join(oTradeDir, e.Name()))
				optionsTradesByDate[ds] = trades
			}
		}
	}
	fmt.Printf("   %d equity, %d options trade files\n\n", len(equityTradesByDate), len(optionsTradesByDate))

	// Merge all dates
	allDates := make(map[string]bool)
	for d := range equityByDate { allDates[d] = true }
	for d := range optionsByDate { allDates[d] = true }
	for d := range equityTradesByDate { allDates[d] = true }
	for d := range optionsTradesByDate { allDates[d] = true }
	dates := obsSortedKeys(allDates) // reuse helper

	fmt.Printf("📝 Writing %d daily notes...\n", len(dates))
	var results []DailyResult
	for _, d := range dates {
		r := writeTradingDaily(d, equityByDate[d], optionsByDate[d],
			equityTradesByDate[d], optionsTradesByDate[d], cfg.OutputDir, cfg.DryRun)
		results = append(results, r)
	}
	fmt.Println()

	fmt.Println("📋 Writing index...")
	if err := writeTradingIndex(results, cfg.OutputDir, cfg.DryRun); err != nil { return err }
	fmt.Println()

	totalPnL := 0.0; totalTrades := 0
	for _, r := range results { totalPnL += r.PnL; totalTrades += r.Trades }
	fmt.Printf("✅ Trading sync complete: %d days, %d trades, %s\n", len(results), totalTrades, fmtPnL(totalPnL))
	if cfg.DryRun { fmt.Println("   (dry run)") }
	return nil
}
