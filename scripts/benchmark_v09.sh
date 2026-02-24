#!/usr/bin/env bash
# v0.9.0 Multi-Model Benchmark — Enrichment + Classification
# Tests 6 models across both features with real data.
set -eo pipefail

CORTEX="$HOME/bin/cortex"

# macOS doesn't have GNU timeout — use perl-based alternative
run_with_timeout() {
  local secs=$1; shift
  perl -e 'alarm shift; exec @ARGV' "$secs" "$@"
}
RESULTS_DIR="/tmp/v09_benchmark_$(date +%Y%m%d_%H%M)"
mkdir -p "$RESULTS_DIR"

MEMORY_DIR="$HOME/clawd/memory"

# ═══════════════════════════════════════════════════
#  TEST FILES (mix of content types)
# ═══════════════════════════════════════════════════
declare -a ENRICH_FILES=(
  "$HOME/clawd/MEMORY.md"
  "$HOME/clawd/USER.md"
  "$HOME/clawd/TOOLS.md"
  "$MEMORY_DIR/2026-02-20.md"
  "$MEMORY_DIR/2026-02-23.md"
)

# ═══════════════════════════════════════════════════
#  MODELS TO BENCHMARK
# ═══════════════════════════════════════════════════
declare -a MODELS=(
  "google/gemini-2.0-flash"
  "openrouter/minimax/minimax-m2.5"
  "google/gemini-3-flash-preview"
  "openrouter/deepseek/deepseek-v3.2"
  "openrouter/x-ai/grok-4.1-fast"
  "openrouter/openai/gpt-5.1-codex-mini"
)

# Cost lookup (simple function instead of associative arrays with slash keys)
get_cost() {
  case "$1" in
    "google/gemini-2.0-flash")        echo "free";;
    "openrouter/minimax/minimax-m2.5") echo "\$0.30/\$1.10";;
    "google/gemini-3-flash-preview")   echo "free";;
    "openrouter/deepseek/deepseek-v3.2") echo "\$0.25/\$0.40";;
    "openrouter/x-ai/grok-4.1-fast")  echo "\$0.20/\$0.50";;
    "openrouter/openai/gpt-5.1-codex-mini") echo "\$0.25/\$2.00";;
    *) echo "?";;
  esac
}

echo "═══════════════════════════════════════════════════════════"
echo "  Cortex v0.9.0 Multi-Model Benchmark"
echo "  $(date '+%Y-%m-%d %H:%M:%S %Z')"
echo "  Results: $RESULTS_DIR"
echo "═══════════════════════════════════════════════════════════"
echo ""

# ─────────────────────────────────────────────────
#  PART 1: ENRICHMENT BENCHMARK
# ─────────────────────────────────────────────────
echo "╔═══════════════════════════════════════════════════════╗"
echo "║  PART 1: ENRICHMENT (extract --enrich)               ║"
echo "╚═══════════════════════════════════════════════════════╝"
echo ""

# First, get baseline rule-only counts (stored in temp file)
RULE_FILE="$RESULTS_DIR/rule_counts.txt"
echo "Baseline (rule-only extraction):"
for file in "${ENRICH_FILES[@]}"; do
  [[ -f "$file" ]] || continue
  fname=$(basename "$file")
  count=$($CORTEX extract "$file" --json 2>/dev/null | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d))" 2>/dev/null || echo "0")
  echo "$fname=$count" >> "$RULE_FILE"
  printf "  %-30s %3d facts\n" "$fname" "$count"
done

get_rule_count() {
  grep "^$1=" "$RULE_FILE" 2>/dev/null | cut -d= -f2 || echo "0"
}
echo ""

# CSV header for enrichment
echo "model,file,rule_facts,llm_facts,new_pct,latency_ms,status" > "$RESULTS_DIR/enrichment.csv"

declare -a ENRICH_SUMMARY=()

for model in "${MODELS[@]}"; do
  echo "━━━ $model ━━━"
  
  total_rule=0
  total_new=0
  total_ms=0
  file_count=0
  errors=0

  for file in "${ENRICH_FILES[@]}"; do
    [[ -f "$file" ]] || continue
    fname=$(basename "$file")
    rule_count=$(get_rule_count "$fname")
    
    out="$RESULTS_DIR/enrich_${fname%.md}_$(echo "$model" | tr '/' '_').json"
    err_out="$RESULTS_DIR/enrich_${fname%.md}_$(echo "$model" | tr '/' '_').err"
    
    start_ms=$(python3 -c "import time; print(int(time.time()*1000))")
    
    if run_with_timeout 60 $CORTEX extract "$file" --enrich --llm "$model" --json > "$out" 2>"$err_out"; then
      end_ms=$(python3 -c "import time; print(int(time.time()*1000))")
      elapsed=$((end_ms - start_ms))
      
      enrich_count=$(python3 -c "import json,sys; print(len(json.load(sys.stdin)))" < "$out" 2>/dev/null || echo "$rule_count")
      new_facts=$((enrich_count - rule_count))
      [[ $new_facts -lt 0 ]] && new_facts=0
      
      pct="0.0"
      [[ $rule_count -gt 0 ]] && pct=$(python3 -c "print(f'{($new_facts/$rule_count)*100:.1f}')")
      
      printf "  %-30s rules:%3d  +llm:%3d  (+%5s%%)  %5dms  ✅\n" "$fname" "$rule_count" "$new_facts" "$pct" "$elapsed"
      echo "$model,$fname,$rule_count,$new_facts,$pct,$elapsed,ok" >> "$RESULTS_DIR/enrichment.csv"
      
      total_rule=$((total_rule + rule_count))
      total_new=$((total_new + new_facts))
      total_ms=$((total_ms + elapsed))
      file_count=$((file_count + 1))
    else
      end_ms=$(python3 -c "import time; print(int(time.time()*1000))")
      elapsed=$((end_ms - start_ms))
      err_msg=$(head -1 "$err_out" 2>/dev/null || echo "timeout/unknown")
      
      printf "  %-30s rules:%3d  FAILED  %5dms  ❌ %s\n" "$fname" "$rule_count" "$elapsed" "$err_msg"
      echo "$model,$fname,$rule_count,0,0.0,$elapsed,FAIL" >> "$RESULTS_DIR/enrichment.csv"
      errors=$((errors + 1))
      file_count=$((file_count + 1))
      total_ms=$((total_ms + elapsed))
    fi
  done

  avg_ms=0
  [[ $file_count -gt 0 ]] && avg_ms=$((total_ms / file_count))
  pct_total="0.0"
  [[ $total_rule -gt 0 ]] && pct_total=$(python3 -c "print(f'{($total_new/$total_rule)*100:.1f}')")
  
  cost=$(get_cost "$model")
  
  echo "  ─── TOTAL: rules=$total_rule +llm=$total_new (+${pct_total}%) avg=${avg_ms}ms errors=$errors cost=$cost/M"
  ENRICH_SUMMARY+=("$model|$total_rule|$total_new|$pct_total|$avg_ms|$errors|$cost")
  echo ""
done

# ─────────────────────────────────────────────────
#  PART 2: CLASSIFICATION BENCHMARK
# ─────────────────────────────────────────────────
echo ""
echo "╔═══════════════════════════════════════════════════════╗"
echo "║  PART 2: CLASSIFICATION (classify --dry-run)         ║"
echo "╚═══════════════════════════════════════════════════════╝"
echo ""

# Get total kv facts available
total_kv=$($CORTEX classify --llm "google/gemini-2.0-flash" --limit 1 --dry-run 2>&1 | grep -o 'Found [0-9]*' | grep -o '[0-9]*' || echo "50")
echo "KV facts available: ~$total_kv (testing 50 per model)"
echo ""

echo "model,reclassified,unchanged,errors,total,batches,latency_ms,status" > "$RESULTS_DIR/classification.csv"

declare -a CLASS_SUMMARY=()

for model in "${MODELS[@]}"; do
  echo "━━━ $model ━━━"
  
  out="$RESULTS_DIR/classify_$(echo "$model" | tr '/' '_').json"
  err_out="$RESULTS_DIR/classify_$(echo "$model" | tr '/' '_').err"
  
  start_ms=$(python3 -c "import time; print(int(time.time()*1000))")
  
  if run_with_timeout 90 $CORTEX classify --llm "$model" --limit 50 --dry-run --json > "$out" 2>"$err_out"; then
    end_ms=$(python3 -c "import time; print(int(time.time()*1000))")
    elapsed=$((end_ms - start_ms))
    
    # Parse JSON result
    eval $(python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    c = d.get('Classified') or d.get('classified') or []
    print(f'reclass={len(c)}')
    print(f'unchanged={d.get(\"Unchanged\", d.get(\"unchanged\", 0))}')
    print(f'errs={d.get(\"Errors\", d.get(\"errors\", 0))}')
    print(f'total={d.get(\"TotalFacts\", d.get(\"total_facts\", 0))}')
    print(f'batches={d.get(\"BatchCount\", d.get(\"batch_count\", 0))}')
    # Type distribution
    types = {}
    for item in c:
        t = item.get('type', item.get('NewType', 'unknown'))
        types[t] = types.get(t, 0) + 1
    print(f'type_dist=\"{types}\"')
except:
    print('reclass=0'); print('unchanged=0'); print('errs=50'); print('total=50'); print('batches=0'); print('type_dist=\"{}\"')
" < "$out" 2>/dev/null)
    
    printf "  reclassified: %d/%d  unchanged: %d  errors: %d  batches: %d  %dms  ✅\n" \
      "$reclass" "$total" "$unchanged" "$errs" "$batches" "$elapsed"
    [[ -n "$type_dist" && "$type_dist" != "{}" ]] && echo "  types: $type_dist"
    echo "$model,$reclass,$unchanged,$errs,$total,$batches,$elapsed,ok" >> "$RESULTS_DIR/classification.csv"
    CLASS_SUMMARY+=("$model|$reclass|$unchanged|$errs|$total|$elapsed|ok")
  else
    end_ms=$(python3 -c "import time; print(int(time.time()*1000))")
    elapsed=$((end_ms - start_ms))
    err_msg=$(tail -2 "$err_out" 2>/dev/null | head -1 || echo "timeout/unknown")
    
    printf "  FAILED  %dms  ❌ %s\n" "$elapsed" "$err_msg"
    echo "$model,0,0,50,50,0,$elapsed,FAIL" >> "$RESULTS_DIR/classification.csv"
    CLASS_SUMMARY+=("$model|0|0|50|50|$elapsed|FAIL")
  fi
  echo ""
done

# ─────────────────────────────────────────────────
#  SUMMARY
# ─────────────────────────────────────────────────
echo ""
echo "╔═══════════════════════════════════════════════════════════════════════════════╗"
echo "║  FINAL COMPARISON                                                           ║"
echo "╚═══════════════════════════════════════════════════════════════════════════════╝"
echo ""

echo "ENRICHMENT:"
printf "  %-45s  rules  +llm  +%%      avg_ms  err  cost(in/out/M)\n" "Model"
printf "  %-45s  ─────  ────  ─────  ──────  ───  ─────────────\n" "─────"
for r in "${ENRICH_SUMMARY[@]}"; do
  IFS='|' read -r m ru en pct av er co <<< "$r"
  printf "  %-45s  %5s  %4s  +%5s%%  %5sms  %3s  %s\n" "$m" "$ru" "$en" "$pct" "$av" "$er" "$co"
done

echo ""
echo "CLASSIFICATION (50 kv facts, dry-run):"
printf "  %-45s  reclass  unch  err  total  latency  status\n" "Model"
printf "  %-45s  ───────  ────  ───  ─────  ───────  ──────\n" "─────"
for r in "${CLASS_SUMMARY[@]}"; do
  IFS='|' read -r m rc un er to la st <<< "$r"
  printf "  %-45s  %7s  %4s  %3s  %5s  %5sms  %s\n" "$m" "$rc" "$un" "$er" "$to" "$la" "$st"
done

echo ""
echo "Raw data: $RESULTS_DIR/"
echo "CSV files: enrichment.csv, classification.csv"
echo "Per-model JSON: enrich_*.json, classify_*.json"
echo ""
echo "Done. $(date '+%H:%M:%S')"
