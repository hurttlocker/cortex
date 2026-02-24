#!/usr/bin/env bash
# Benchmark enrichment with real files across two models.
set -euo pipefail

CORTEX="$(cd "$(dirname "$0")/.." && pwd)/cortex"
RESULTS_DIR="/tmp/enrich_benchmark"
mkdir -p "$RESULTS_DIR"

MEMORY_DIR="$HOME/clawd/memory"

# Test files — mix of types per #218 spec
declare -a FILES=(
  "$HOME/clawd/MEMORY.md"
  "$HOME/clawd/USER.md"
  "$HOME/clawd/TOOLS.md"
  "$MEMORY_DIR/2026-02-20.md"
  "$MEMORY_DIR/2026-02-23.md"
)

declare -a MODELS=(
  "google/gemini-2.0-flash"
  "openrouter/openai/gpt-5.1-codex-mini"
)

echo "═══════════════════════════════════════════════════"
echo "  Cortex Enrichment Benchmark — $(date '+%Y-%m-%d %H:%M')"
echo "═══════════════════════════════════════════════════"
echo ""

# Collect all results for summary
declare -a ALL_RESULTS=()

for model in "${MODELS[@]}"; do
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "  Model: $model"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  
  total_rule=0
  total_enrich=0
  total_time_ms=0
  file_count=0

  for file in "${FILES[@]}"; do
    [[ -f "$file" ]] || { echo "  SKIP: $file"; continue; }
    
    fname=$(basename "$file")
    fsize=$(wc -c < "$file" | tr -d ' ')
    
    # Rule-only count
    rule_count=$($CORTEX extract "$file" --json 2>/dev/null | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")

    # Enriched extraction
    out="$RESULTS_DIR/${fname%.md}_${model//\//_}.json"
    stderr_out="$RESULTS_DIR/${fname%.md}_${model//\//_}.stderr"
    start_ms=$(python3 -c "import time; print(int(time.time()*1000))")
    $CORTEX extract "$file" --enrich --llm "$model" --json > "$out" 2>"$stderr_out" || true
    end_ms=$(python3 -c "import time; print(int(time.time()*1000))")
    elapsed=$((end_ms - start_ms))
    
    enrich_count=$(python3 -c "import json,sys; print(len(json.load(sys.stdin)))" < "$out" 2>/dev/null || echo "$rule_count")
    new_facts=$((enrich_count - rule_count))
    [[ $new_facts -lt 0 ]] && new_facts=0
    
    pct="0.0"
    [[ $rule_count -gt 0 ]] && pct=$(python3 -c "print(f'{($new_facts/$rule_count)*100:.1f}')")

    printf "  %-30s rules:%3d  +llm:%3d  (+%5s%%)  %5dms\n" "$fname" "$rule_count" "$new_facts" "$pct" "$elapsed"
    
    total_rule=$((total_rule + rule_count))
    total_enrich=$((total_enrich + new_facts))
    total_time_ms=$((total_time_ms + elapsed))
    file_count=$((file_count + 1))
  done

  avg_ms=0
  [[ $file_count -gt 0 ]] && avg_ms=$((total_time_ms / file_count))
  pct_total="0.0"
  [[ $total_rule -gt 0 ]] && pct_total=$(python3 -c "print(f'{($total_enrich/$total_rule)*100:.1f}')")

  echo ""
  echo "  TOTAL: rules=$total_rule +llm=$total_enrich (+${pct_total}%) avg=${avg_ms}ms/file"
  ALL_RESULTS+=("$model|$total_rule|$total_enrich|$pct_total|$avg_ms")
  echo ""
done

echo "═══════════════════════════════════════════════════"
echo "  COMPARISON"
echo "═══════════════════════════════════════════════════"
printf "  %-40s  rules  +llm  +%%      avg_ms\n" "Model"
for r in "${ALL_RESULTS[@]}"; do
  IFS='|' read -r m ru en pct av <<< "$r"
  printf "  %-40s  %5s  %4s  +%5s%%  %5sms\n" "$m" "$ru" "$en" "$pct" "$av"
done
echo ""
