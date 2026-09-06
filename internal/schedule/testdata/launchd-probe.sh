#!/bin/sh
# Only used by the opt-in native acceptance test. No network or CLI actions.
set -eu
[ "$#" -eq 3 ] && [ "$1" = --data-dir ] && [ "$3" = scheduled-run ]
probe_data=$2
printf '%s start\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$probe_data/events.log"
if [ -f "$probe_data/hold" ]; then
  printf '%s holding\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$probe_data/events.log"
  sleep 25
fi
