printf '%s\n' tick >> ticks.txt
echo beat
if [ "$(wc -l < ticks.txt)" -ge 30 ]; then touch done; fi
