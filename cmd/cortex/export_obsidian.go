package main

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

// ── Config ──────────────────────────────────────────────────────────────

const (
	defaultMinRefs        = 5
	defaultConceptMinRefs = 10
	defaultConceptMinOut  = 2
	defaultMaxNameLen     = 45
)

type TopicPattern struct {
	Pattern *regexp.Regexp
	Topic   string
}

type HubTypePattern struct {
	Pattern *regexp.Regexp
	HubType string
}

type EntityHub struct {
	Name     string
	HubType  string
	Facts    []*store.Fact
	Topics   map[string]bool
	RefCount int
}

type ObsidianExportConfig struct {
	VaultRoot      string
	OutputDir      string
	DryRun         bool
	Clean          bool
	MinRefs        int
	ConceptMinRefs int
	ConceptMinOut  int
	MaxNameLen     int
	Validate       bool
}

// ── Patterns ────────────────────────────────────────────────────────────

var obsTopicPatterns = []TopicPattern{
	{regexp.MustCompile(`(?i)trading|ORB|Triple Crown|scanner|Alpaca|Public\.com|backtest|options|crypto|ADA|SRB|ML220|P&L|scorecard|forward.test|equity.curve`), "Trading"},
	{regexp.MustCompile(`(?i)Spear|HHA|RustDesk|PayPal|customer|device|spear-production`), "Spear"},
	{regexp.MustCompile(`(?i)Eyes Web|antiflammi|Flammi|onboarding|dossier|meal|scleritis|mybeautifulwife`), "Eyes Web"},
	{regexp.MustCompile(`(?i)Cortex|cortex|memory.layer|search.fact|import|embed|sync.fact|MCP|belief.lifecycle|enrichment|governor`), "Cortex"},
	{regexp.MustCompile(`(?i)YouTube|video|thumbnail|Remotion|TTS|upload|pipeline|VEO|Kling`), "YouTube"},
	{regexp.MustCompile(`(?i)eBay|David Yurman|listing|jewelry|retailwhat`), "eBay"},
	{regexp.MustCompile(`(?i)Discord|Telegram|OpenClaw|gateway|cron|heartbeat|agent|Mister|Niot|Hawk|Sage|Noemie|launchd`), "System & Agents"},
	{regexp.MustCompile(`(?i)wedding|SB|Sydney|Lemon|fiancée|registry`), "Personal"},
	{regexp.MustCompile(`(?i)Parallel|marketplace|P2P`), "Parallel"},
	{regexp.MustCompile(`(?i)copy.trade|sleeping.beauties|SnapTrade`), "Copy Trade"},
	{regexp.MustCompile(`(?i)Obsidian|vault|wikilink|graph.view|CLI`), "Obsidian"},
}

var obsHubTypePatterns = []HubTypePattern{
	{regexp.MustCompile(`(?i)^(Q|SB|Niot|Hawk|Sage|Mister|Noemie|Voss|Sydney|Marquise|x7|coldgame|cashcoldgame)$`), "person"},
	{regexp.MustCompile(`(?i)^(Spear|Eyes Web|Parallel|Cortex|eBay|Copy Trade|Sleeping Beauties|Engram|OpenClaw|Mission Control)$`), "project"},
	{regexp.MustCompile(`(?i)^(Triple Crown|ORB|ML220|SRB|ADA ML220|Session Range Breakout|EMA strategy)$`), "strategy"},
	{regexp.MustCompile(`(?i)^(Alpaca|Public\.com|Coinbase|Finnhub|Discord|Telegram|Railway|Vercel|RustDesk|PayPal|GitHub|Obsidian)$`), "system"},
}

var obsEntityBlocklist = []*regexp.Regexp{
	regexp.MustCompile(`^(hawk|main|ace)_[0-9a-f]{6,}`),
	regexp.MustCompile(`(?i)^(him|her|it|we|they|them|us|you|I)$`),
	regexp.MustCompile(`(?i)^(user|bot|agent|message|sender|fix|test|graph|epic|system|process|issues|team|facts|comments|workflow|conversation|repo|task|audit|plan|secrets|connectors|speaker)$`),
	regexp.MustCompile(`(?i)^(Comment|feat|chore|bug|ci|fix|docs|test|meta|sprint)\b`),
	regexp.MustCompile(`(?i)^(PART|Step|Session|Phase|Updated|Queued|Context|Output|Rules|Status|Assessment)\b`),
	regexp.MustCompile(`^msg\d+$`),
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}`),
	regexp.MustCompile(`^[A-Z][A-Z\s&]{15,}$`),
	regexp.MustCompile(`@|\.com|\.md$|http|localhost|^PR #|^#\d|^v\d`),
	regexp.MustCompile(`^\d`),
	regexp.MustCompile(`^[a-z]{1,3}$`),
	regexp.MustCompile(`\.(py|sh|json|yaml)$`),
	regexp.MustCompile(`^com\.`),
	regexp.MustCompile(`(?i)SHIPPED|DEPLOYED|LOCKED|COMPLETE|BUILT`),
	regexp.MustCompile(`v\d+\.\d+`),
	regexp.MustCompile(`(?i)Sprint|Session|Audit|Upgrade|Migration|Hardening|Cleanup|Research`),
	regexp.MustCompile(`(?i)Epic|Tier \d|Phase \d|Wave`),
	regexp.MustCompile(`\((Feb|Jan|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)`),
	regexp.MustCompile(`(?i)Progress\b|Shipped\b|Fixed\b`),
	regexp.MustCompile(`^.{45,}$`),
	regexp.MustCompile(`(?i)\b(the|a|an|is|are|was|were|has|have|had)\b.*\b(the|a|an|is|are|was|were|has|have|had)\b`),
	regexp.MustCompile(`(?i)^(developer|Wednesday|Monday|Tuesday|Thursday|Friday|Saturday|Sunday|Scanner|MacBook|iMac|machine|opus|Relay|Scorecard|pipeline|platform|Reddit)$`),
	regexp.MustCompile(`(?i)^(COMPLETED TODAY|DAILY SCORECARD|TODO|EXECUTIVE|EXECUTION|MEMORY_ALERT|IDENTITY)$`),
	regexp.MustCompile(`(?i)Backtester|Wiring|Ghost|Truncation|Burn-in`),
	regexp.MustCompile(`^Task #\d+`),
	regexp.MustCompile(`(?i)^(lastChecks|SHORT|developer|opus|cortex)$`),
	regexp.MustCompile(`(?i)^(Quality bar|X Pulse|High-upside|TODO|CORTEX)`),
	regexp.MustCompile(`#\d+|Issue #|Epic #`),
	regexp.MustCompile(`(?i)Connector|Plugin|Implement|SearchEmbedding`),
	regexp.MustCompile(`^hurttlocker`),
	regexp.MustCompile(`(?i)recommended$|Updated$|Installed$|Created$|Launched$|Dispatched$|JOURNAL`),
	regexp.MustCompile(`^misterexc7$`),
	regexp.MustCompile(`(?i)^xmate\b`),
	regexp.MustCompile(`(?i)^Mister Labs System`),
	regexp.MustCompile(`(?i)ATTOM|Vercel MCP|Greeks Replay|Next Actions|Sydney.s machine|Codex 5\.3`),
	regexp.MustCompile(`(?i)Agentic Wallet|Crypto Module`),
	regexp.MustCompile(`(?i)^(mister|MISTER)$`),
	regexp.MustCompile(`(?i)^(Active Projects|Delivery|Pre-Compaction|Core File|Agent Restructure|Who|Bottom line)\b`),
	regexp.MustCompile(`(?i)What To Do|what you asked|For Q|from Reddit`),
	regexp.MustCompile(`(?i)^(Feb|Jan|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d`),
	regexp.MustCompile(`(?i)PUBLIC\.COM LIVE|Hawk QA|How It Works|Webhook delivery|Smarter Fact|API Rank|Mister Copy`),
	regexp.MustCompile(`(?i)Relationship inference|Obsidian CLI Integration`),
	regexp.MustCompile(`(?i)^Move-in|^What I need|Multi-Agent Architecture`),
}

var obsEntityAllowlist = map[string]bool{
	"Q": true, "SB": true, "Mister": true, "Niot": true, "Hawk": true, "Noemie": true, "x7": true,
	"Triple Crown": true, "ORB": true, "ML220": true, "ADA ML220": true, "SRB": true,
	"Spear": true, "Eyes Web": true, "Cortex": true, "Parallel": true, "Engram": true, "OpenClaw": true,
	"Alpaca": true, "Public.com": true, "Coinbase": true, "Finnhub": true, "Discord": true, "Obsidian": true,
	"QQQ": true, "SPY": true, "ADA": true, "SOL": true, "ETH": true,
}

var obsTypeEmoji = map[string]string{
	"decision": "⚖️", "config": "⚙️", "identity": "👤", "relationship": "🤝",
	"state": "📍", "temporal": "📅", "kv": "📝", "preference": "💜",
	"location": "📍", "rule": "📏", "status": "📊", "scratch": "✏️",
}

var obsHubTypeIcons = map[string]string{
	"person": "👤", "project": "📦", "strategy": "🎯", "system": "⚙️", "concept": "💡",
}

// ── Core Functions ──────────────────────────────────────────────────────

func obsClassifyTopic(text string) string {
	for _, tp := range obsTopicPatterns {
		if tp.Pattern.MatchString(text) {
			return tp.Topic
		}
	}
	return "General"
}

func obsClassifyHubType(name string) string {
	for _, hp := range obsHubTypePatterns {
		if hp.Pattern.MatchString(name) {
			return hp.HubType
		}
	}
	return "concept"
}

func obsIsBlocked(name string) bool {
	for _, p := range obsEntityBlocklist {
		if p.MatchString(name) {
			return true
		}
	}
	return false
}

func obsConfBar(conf float64) string {
	if conf >= 0.9 {
		return "🟢"
	}
	if conf >= 0.7 {
		return "🟡"
	}
	if conf >= 0.5 {
		return "🟠"
	}
	return "🔴"
}

func obsSanitize(name string) string {
	r := strings.NewReplacer("<", "", ">", "", ":", "", "\"", "", "/", "", "\\", "", "|", "", "?", "", "*", "")
	c := r.Replace(name)
	if len(c) > 80 {
		c = c[:80]
	}
	return strings.TrimSpace(c)
}

func obsEmoji(ft string) string {
	if e, ok := obsTypeEmoji[ft]; ok {
		return e
	}
	return "📝"
}

func obsSortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ── Entity Index ────────────────────────────────────────────────────────

func obsBuildEntityIndex(facts []*store.Fact) (map[string][]*store.Fact, map[string]map[string]bool) {
	ef := make(map[string][]*store.Fact)
	et := make(map[string]map[string]bool)
	for _, fact := range facts {
		subj := strings.TrimSpace(fact.Subject)
		if subj == "" || len(subj) < 2 || len(subj) > 80 {
			continue
		}
		if m, _ := regexp.MatchString(`^[\d./$~\\]`, subj); m {
			continue
		}
		if !obsEntityAllowlist[subj] && obsIsBlocked(subj) {
			continue
		}
		combined := subj + " " + fact.Predicate + " " + fact.Object + " " + fact.SourceQuote
		topic := obsClassifyTopic(combined)
		ef[subj] = append(ef[subj], fact)
		if et[subj] == nil {
			et[subj] = make(map[string]bool)
		}
		et[subj][topic] = true
	}
	return ef, et
}

func obsSelectHubs(ef map[string][]*store.Fact, et map[string]map[string]bool, cfg ObsidianExportConfig) map[string]*EntityHub {
	hubs := make(map[string]*EntityHub)
	for entity, facts := range ef {
		ht := obsClassifyHubType(entity)
		al := obsEntityAllowlist[entity]
		thresh := cfg.MinRefs
		if al {
			thresh = 1
		} else if ht == "concept" {
			thresh = cfg.ConceptMinRefs
		}
		if len(facts) >= thresh {
			topics := et[entity]
			if topics == nil {
				topics = make(map[string]bool)
			}
			hubs[entity] = &EntityHub{Name: entity, HubType: ht, Facts: facts, Topics: topics, RefCount: len(facts)}
		}
	}
	return hubs
}

func obsPruneWeak(hubs map[string]*EntityHub, minOut int) int {
	hubNames := make(map[string]bool)
	for n := range hubs {
		hubNames[n] = true
	}
	var rem []string
	for entity, hub := range hubs {
		if hub.HubType != "concept" || obsEntityAllowlist[entity] {
			continue
		}
		out := make(map[string]bool)
		for t := range hub.Topics {
			out[t] = true
		}
		for _, f := range hub.Facts {
			for oh := range hubNames {
				if oh != entity && strings.Contains(f.Object, oh) {
					out[oh] = true
				}
			}
		}
		if len(out) < minOut {
			rem = append(rem, entity)
		}
	}
	for _, e := range rem {
		delete(hubs, e)
	}
	return len(rem)
}

// ── Note Writers ────────────────────────────────────────────────────────

func obsWriteEntity(entity string, hub *EntityHub, outDir string, dryRun bool) error {
	fn := obsSanitize(entity) + ".md"
	dir := filepath.Join(outDir, "entities")
	path := filepath.Join(dir, fn)
	icon := obsHubTypeIcons[hub.HubType]
	if icon == "" {
		icon = "💡"
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "synced: %s\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "hub_type: %s\nrefs: %d\n", hub.HubType, hub.RefCount)
	tl := obsSortedKeys(hub.Topics)
	fmt.Fprintf(&b, "topics: [%s]\n", strings.Join(tl, ", "))
	fmt.Fprintf(&b, "tags: [\"#cortex\", \"#hub/%s\"]\n---\n\n", hub.HubType)
	fmt.Fprintf(&b, "# %s %s\n\n> **%d references** · Type: `%s` · From [[Cortex Dashboard]]\n\n", icon, entity, hub.RefCount, hub.HubType)

	if len(tl) > 0 {
		links := make([]string, len(tl))
		for i, t := range tl {
			links[i] = "[[" + t + "]]"
		}
		b.WriteString("**Appears in:** " + strings.Join(links, " · ") + "\n\n")
	}

	// Group by predicate
	byPred := make(map[string][]*store.Fact)
	for _, f := range hub.Facts {
		p := f.Predicate
		if p == "" {
			p = "related to"
		}
		byPred[p] = append(byPred[p], f)
	}
	type pg struct {
		p  string
		fs []*store.Fact
	}
	var pgs []pg
	for p, fs := range byPred {
		pgs = append(pgs, pg{p, fs})
	}
	sort.Slice(pgs, func(i, j int) bool { return len(pgs[i].fs) > len(pgs[j].fs) })

	for _, g := range pgs {
		fmt.Fprintf(&b, "### %s\n\n", g.p)
		sort.Slice(g.fs, func(i, j int) bool { return g.fs[i].Confidence > g.fs[j].Confidence })
		for _, f := range g.fs {
			st := ""
			if f.State != "active" && f.State != "" {
				st = fmt.Sprintf(" `%s`", f.State)
			}
			fmt.Fprintf(&b, "- %s %s %s %.0f%%%s\n", obsEmoji(f.FactType), f.Object, obsConfBar(f.Confidence), f.Confidence*100, st)
			if f.SourceQuote != "" {
				q := f.SourceQuote
				if len(q) > 200 {
					q = q[:200]
				}
				fmt.Fprintf(&b, "  > _%s_\n", q)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Related\n\n- [[Cortex Dashboard]]\n")
	for _, t := range tl {
		fmt.Fprintf(&b, "- [[%s]]\n", t)
	}
	b.WriteString("\n")

	if dryRun {
		fmt.Printf("  📄 Would write: entities/%s (%d refs, %s)\n", fn, hub.RefCount, hub.HubType)
		return nil
	}
	os.MkdirAll(dir, 0755)
	return os.WriteFile(path, []byte(b.String()), 0644)
}

func obsWriteTopic(topic string, facts []*store.Fact, hubs map[string]*EntityHub, outDir string, dryRun bool) (int, error) {
	fn := obsSanitize(topic) + ".md"
	dir := filepath.Join(outDir, "topics")
	path := filepath.Join(dir, fn)

	te := make(map[string]*EntityHub)
	for e, h := range hubs {
		if h.Topics[topic] {
			te[e] = h
		}
	}

	byType := make(map[string][]*store.Fact)
	for _, f := range facts {
		ft := f.FactType
		if ft == "" {
			ft = "kv"
		}
		byType[ft] = append(byType[ft], f)
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "synced: %s\nfacts: %d\nentities: %d\nsource: cortex\n", time.Now().Format("2006-01-02 15:04"), len(facts), len(te))
	fmt.Fprintf(&b, "tags: [\"#cortex\", \"#cortex/%s\"]\n---\n\n", strings.ToLower(strings.ReplaceAll(topic, " ", "-")))
	fmt.Fprintf(&b, "# %s\n\n> 🧠 **%d facts** · **%d entities** · From [[Cortex Dashboard]] · Synced %s\n\n", topic, len(facts), len(te), time.Now().Format("Jan 02, 2006"))

	if len(te) > 0 {
		b.WriteString("## Key Entities\n\n")
		en := make(map[string]bool)
		for e := range te {
			en[e] = true
		}
		for _, e := range obsSortedKeys(en) {
			h := te[e]
			fmt.Fprintf(&b, "- %s [[%s]] (%d refs)\n", obsHubTypeIcons[h.HubType], e, h.RefCount)
		}
		b.WriteString("\n")
	}

	type tg struct {
		n  string
		fs []*store.Fact
	}
	var tgs []tg
	for n, fs := range byType {
		tgs = append(tgs, tg{n, fs})
	}
	sort.Slice(tgs, func(i, j int) bool { return len(tgs[i].fs) > len(tgs[j].fs) })

	for _, g := range tgs {
		fmt.Fprintf(&b, "## %s %s (%d)\n\n", obsEmoji(g.n), strings.Title(g.n), len(g.fs))
		sort.Slice(g.fs, func(i, j int) bool { return g.fs[i].Confidence > g.fs[j].Confidence })
		for _, f := range g.fs {
			c := fmt.Sprintf("**%s** %s → %s", f.Subject, f.Predicate, f.Object)
			if len(c) > 500 {
				c = c[:497] + "..."
			}
			for e := range te {
				if strings.Contains(c, e) && !strings.Contains(c, "[["+e+"]]") {
					c = strings.Replace(c, e, "[["+e+"]]", 1)
				}
			}
			st := ""
			if f.State != "active" && f.State != "" {
				st = fmt.Sprintf(" `%s`", f.State)
			}
			fmt.Fprintf(&b, "- %s\n  - *%s %.0f%%%s*\n\n", c, obsConfBar(f.Confidence), f.Confidence*100, st)
		}
	}

	b.WriteString("## Related Topics\n\n- [[Cortex Dashboard]]\n")
	rt := make(map[string]bool)
	for _, h := range te {
		for t := range h.Topics {
			if t != topic {
				rt[t] = true
			}
		}
	}
	for _, t := range obsSortedKeys(rt) {
		fmt.Fprintf(&b, "- [[%s]]\n", t)
	}
	b.WriteString("\n")

	if dryRun {
		fmt.Printf("  📄 Would write: topics/%s (%d facts, %d entities)\n", fn, len(facts), len(te))
		return len(te), nil
	}
	os.MkdirAll(dir, 0755)
	return len(te), os.WriteFile(path, []byte(b.String()), 0644)
}

type obsTopicResult struct {
	name                   string
	factCount, entityCount int
}

func obsWriteDashboard(trs []obsTopicResult, hubs map[string]*EntityHub, outDir string, dryRun bool) error {
	path := filepath.Join(outDir, "Cortex Dashboard.md")
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "synced: %s\ntags: [\"#cortex\", \"#cortex/dashboard\", \"#MOC\"]\n---\n\n# 🧠 Cortex Dashboard\n\n", time.Now().Format("2006-01-02 15:04"))

	tf := 0
	for _, tr := range trs {
		tf += tr.factCount
	}
	fmt.Fprintf(&b, "> **%d facts** · **%d entity hubs** · **%d topics** · Last sync: %s\n\n", tf, len(hubs), len(trs), time.Now().Format("Jan 02, 2006 3:04 PM"))

	b.WriteString("## Topics\n\n")
	sort.Slice(trs, func(i, j int) bool { return trs[i].factCount > trs[j].factCount })
	for _, tr := range trs {
		fmt.Fprintf(&b, "- [[%s]] — %d facts, %d entities\n", tr.name, tr.factCount, tr.entityCount)
	}
	b.WriteString("\n## Entity Hubs\n\n")

	bht := make(map[string][]*EntityHub)
	for _, h := range hubs {
		bht[h.HubType] = append(bht[h.HubType], h)
	}
	for _, ht := range []string{"person", "project", "strategy", "system", "concept"} {
		es := bht[ht]
		if len(es) == 0 {
			continue
		}
		sort.Slice(es, func(i, j int) bool { return es[i].RefCount > es[j].RefCount })
		fmt.Fprintf(&b, "### %s %ss\n\n", obsHubTypeIcons[ht], strings.Title(ht))
		for _, h := range es {
			fmt.Fprintf(&b, "- [[%s]] (%d refs)\n", h.Name, h.RefCount)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Workspace\n\n- [[MEMORY]]\n- [[SOUL]]\n- [[AGENTS]]\n- [[TOOLS]]\n\n---\n")
	fmt.Fprintf(&b, "*Auto-synced by `cortex export obsidian` v1.3 · %s*\n", time.Now().Format("2006-01-02 15:04"))

	if dryRun {
		fmt.Printf("  📄 Would write: Cortex Dashboard.md (%d hubs, %d topics)\n", len(hubs), len(trs))
		return nil
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// ── Main Export Function ────────────────────────────────────────────────

func runExportObsidian(args []string) error {
	// Check for --trading subcommand
	for _, a := range args {
		if a == "--trading" {
			// Strip --trading from args and pass rest to trading export
			var tArgs []string
			for _, x := range args {
				if x != "--trading" {
					tArgs = append(tArgs, x)
				}
			}
			return runExportTrading(tArgs)
		}
	}
	cfg := ObsidianExportConfig{
		MinRefs: defaultMinRefs, ConceptMinRefs: defaultConceptMinRefs,
		ConceptMinOut: defaultConceptMinOut, MaxNameLen: defaultMaxNameLen,
	}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--vault" && i+1 < len(args):
			i++
			cfg.VaultRoot = args[i]
		case strings.HasPrefix(args[i], "--vault="):
			cfg.VaultRoot = strings.TrimPrefix(args[i], "--vault=")
		case args[i] == "--output" && i+1 < len(args):
			i++
			cfg.OutputDir = args[i]
		case strings.HasPrefix(args[i], "--output="):
			cfg.OutputDir = strings.TrimPrefix(args[i], "--output=")
		case args[i] == "--dry-run" || args[i] == "-n":
			cfg.DryRun = true
		case args[i] == "--clean":
			cfg.Clean = true
		case args[i] == "--min-refs" && i+1 < len(args):
			i++
			fmt.Sscanf(args[i], "%d", &cfg.MinRefs)
		case args[i] == "--concept-min-refs" && i+1 < len(args):
			i++
			fmt.Sscanf(args[i], "%d", &cfg.ConceptMinRefs)
		case args[i] == "--trading":
			continue
		case args[i] == "--validate":
			cfg.Validate = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if cfg.VaultRoot == "" {
		cfg.VaultRoot, _ = os.Getwd()
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = filepath.Join(cfg.VaultRoot, "_cortex")
	}

	fmt.Println("🧠 Cortex → Obsidian Export")
	fmt.Printf("   Vault: %s\n   Output: %s\n   Thresholds: ≥%d refs (concepts: ≥%d)\n", cfg.VaultRoot, cfg.OutputDir, cfg.MinRefs, cfg.ConceptMinRefs)
	if cfg.Validate {
		fmt.Println("   Validation: enabled (--validate)")
	}
	fmt.Println()

	if cfg.Clean && !cfg.DryRun {
		os.RemoveAll(cfg.OutputDir)
		fmt.Println("  🗑️  Cleaned output directory")
	}
	if !cfg.DryRun {
		for _, d := range []string{cfg.OutputDir, filepath.Join(cfg.OutputDir, "topics"), filepath.Join(cfg.OutputDir, "entities")} {
			os.MkdirAll(d, 0755)
		}
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()
	ctx := context.Background()

	fmt.Println("🔍 Loading facts...")
	all, err := s.ListFacts(ctx, store.ListOpts{Limit: math.MaxInt32})
	if err != nil {
		return fmt.Errorf("listing facts: %w", err)
	}
	var active []*store.Fact
	for _, f := range all {
		if f.State == "active" || f.State == "core" || f.State == "" {
			active = append(active, f)
		}
	}
	fmt.Printf("   %d active facts\n", len(active))

	fmt.Println("🔗 Building entity index...")
	ef, et := obsBuildEntityIndex(active)
	hubs := obsSelectHubs(ef, et, cfg)
	pre := len(hubs)
	pruned := obsPruneWeak(hubs, cfg.ConceptMinOut)
	fmt.Printf("   %d entities → %d candidates → %d hubs (%d pruned)\n", len(ef), pre, len(hubs), pruned)

	fmt.Println("📂 Grouping by topic...")
	topics := make(map[string][]*store.Fact)
	for _, f := range active {
		t := obsClassifyTopic(f.Subject + " " + f.Predicate + " " + f.Object + " " + f.SourceQuote)
		topics[t] = append(topics[t], f)
	}
	tf := 0
	for _, fs := range topics {
		tf += len(fs)
	}
	fmt.Printf("   %d facts across %d topics\n\n", tf, len(topics))

	fmt.Println("📝 Writing entity hubs...")
	hl := make([]*EntityHub, 0, len(hubs))
	for _, h := range hubs {
		hl = append(hl, h)
	}
	sort.Slice(hl, func(i, j int) bool { return hl[i].RefCount > hl[j].RefCount })
	for _, h := range hl {
		if err := obsWriteEntity(h.Name, h, cfg.OutputDir, cfg.DryRun); err != nil {
			return err
		}
		if !cfg.DryRun {
			fmt.Printf("  ✅ entities/%s.md — %d refs (%s)\n", obsSanitize(h.Name), h.RefCount, h.HubType)
		}
	}
	fmt.Printf("   %d entity hubs\n\n", len(hubs))

	fmt.Println("📝 Writing topic notes...")
	type te struct {
		n  string
		fs []*store.Fact
	}
	var tes []te
	for n, fs := range topics {
		tes = append(tes, te{n, fs})
	}
	sort.Slice(tes, func(i, j int) bool { return len(tes[i].fs) > len(tes[j].fs) })
	var trs []obsTopicResult
	for _, t := range tes {
		ec, err := obsWriteTopic(t.n, t.fs, hubs, cfg.OutputDir, cfg.DryRun)
		if err != nil {
			return err
		}
		if !cfg.DryRun {
			fmt.Printf("  ✅ topics/%s.md — %d facts, %d entities\n", obsSanitize(t.n), len(t.fs), ec)
		}
		trs = append(trs, obsTopicResult{t.n, len(t.fs), ec})
	}
	fmt.Println()

	fmt.Println("📋 Writing dashboard...")
	if err := obsWriteDashboard(trs, hubs, cfg.OutputDir, cfg.DryRun); err != nil {
		return err
	}
	fmt.Println()

	htc := make(map[string]int)
	for _, h := range hubs {
		htc[h.HubType]++
	}
	var ts []string
	for t, c := range htc {
		ts = append(ts, fmt.Sprintf("%s: %d", t, c))
	}
	sort.Strings(ts)
	fmt.Printf("✅ Export complete: %d facts → %d topics + %d hubs\n   Types: %s\n", tf, len(topics), len(hubs), strings.Join(ts, ", "))
	if cfg.DryRun {
		fmt.Println("   (dry run)")
	}

	if cfg.Validate {
		report, err := obsValidateOutput(cfg.OutputDir)
		if err != nil {
			return fmt.Errorf("validate export output: %w", err)
		}
		obsPrintValidationReport(report)
		if report.BrokenLinks > 0 || report.MissingDashboardLinks > 0 || report.Orphans > 0 {
			return fmt.Errorf("obsidian validation failed: broken=%d missing_dashboard=%d orphans=%d", report.BrokenLinks, report.MissingDashboardLinks, report.Orphans)
		}
	}

	return nil
}

var obsWikilinkPattern = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]+)?(?:\|[^\]]+)?\]\]`)

// obsValidationReport captures post-export graph/sync health metrics.
type obsValidationReport struct {
	Root                  string
	Files                 int
	BrokenLinks           int
	MissingDashboardLinks int
	LowOutbound           int
	Orphans               int
	Unresolved            int
	AvgOutgoing           float64
	AvgIncoming           float64
}

func obsValidateOutput(root string) (obsValidationReport, error) {
	report := obsValidationReport{Root: root}
	if root == "" {
		return report, fmt.Errorf("empty output root")
	}
	root = filepath.Clean(root)
	report.Root = root

	dashboard := filepath.Join(root, "Cortex Dashboard.md")
	if _, err := os.Stat(dashboard); err != nil {
		return report, fmt.Errorf("missing dashboard: %w", err)
	}

	files := make(map[string]string)
	base := make(map[string][]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = string(b)
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)))
		base[stem] = append(base[stem], rel)
		return nil
	})
	if err != nil {
		return report, err
	}
	if len(files) == 0 {
		return report, fmt.Errorf("no markdown files under %s", root)
	}
	report.Files = len(files)

	incoming := make(map[string]int)
	outgoing := make(map[string]int)

	for rel, content := range files {
		hasDashboard := rel == "Cortex Dashboard.md"
		matches := obsWikilinkPattern.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			raw := strings.TrimSpace(m[1])
			if raw == "" {
				continue
			}
			resolved, explicit := obsResolveInternalWikilink(raw, files, base)
			if strings.EqualFold(raw, "Cortex Dashboard") {
				hasDashboard = true
			}
			if resolved != "" {
				outgoing[rel]++
				incoming[resolved]++
			} else if explicit {
				report.BrokenLinks++
			}
		}

		if rel != "Cortex Dashboard.md" && !hasDashboard {
			report.MissingDashboardLinks++
		}
		if outgoing[rel] < 2 {
			report.LowOutbound++
		}
	}

	var totalOut, totalIn int
	for rel := range files {
		totalOut += outgoing[rel]
		totalIn += incoming[rel]
		if outgoing[rel] == 0 && incoming[rel] == 0 {
			report.Orphans++
		}
	}
	report.Unresolved = report.BrokenLinks
	report.AvgOutgoing = float64(totalOut) / float64(report.Files)
	report.AvgIncoming = float64(totalIn) / float64(report.Files)

	return report, nil
}

func obsResolveInternalWikilink(raw string, files map[string]string, base map[string][]string) (resolved string, explicitInternal bool) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return "", false
	}
	if strings.EqualFold(raw, "Cortex Dashboard") {
		if _, ok := files["Cortex Dashboard.md"]; ok {
			return "Cortex Dashboard.md", true
		}
		return "", true
	}

	if strings.HasPrefix(raw, "_cortex/") || strings.HasPrefix(raw, "topics/") || strings.HasPrefix(raw, "entities/") {
		explicitInternal = true
		cand := strings.TrimPrefix(raw, "_cortex/")
		if !strings.HasSuffix(strings.ToLower(cand), ".md") {
			cand += ".md"
		}
		cand = filepath.ToSlash(filepath.Clean(cand))
		if _, ok := files[cand]; ok {
			return cand, true
		}
		return "", true
	}

	if strings.Contains(raw, "/") {
		cand := raw
		if !strings.HasSuffix(strings.ToLower(cand), ".md") {
			cand += ".md"
		}
		cand = filepath.ToSlash(filepath.Clean(cand))
		if _, ok := files[cand]; ok {
			return cand, true
		}
		// path-like links are considered explicit internal under _cortex export
		return "", true
	}

	stem := strings.ToLower(raw)
	if hits := base[stem]; len(hits) > 0 {
		sort.Strings(hits)
		return hits[0], false
	}

	// likely external workspace note (MEMORY/SOUL/etc.)
	return "", false
}

func obsPrintValidationReport(r obsValidationReport) {
	fmt.Println("🩺 Validation report")
	fmt.Printf("   root: %s\n", r.Root)
	fmt.Printf("   files: %d\n", r.Files)
	fmt.Printf("   avg_outgoing_links_per_file: %.2f\n", r.AvgOutgoing)
	fmt.Printf("   avg_incoming_links_per_file: %.2f\n", r.AvgIncoming)
	fmt.Printf("   low_outbound_files(<2): %d\n", r.LowOutbound)
	fmt.Printf("   orphans(no in+out): %d\n", r.Orphans)
	fmt.Printf("   broken_wikilinks: %d\n", r.BrokenLinks)
	fmt.Printf("   missing_dashboard_links: %d\n", r.MissingDashboardLinks)
}
