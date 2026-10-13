#!/bin/bash
# Kills review processes (cmdline under the scratchpad rev dirs, or docker qemu
# runs we started) that grow past PER_KB, or the largest one when the host's
# MemAvailable drops below FLOOR_KB. Logs every kill to memwatch.log.
S=${SCRATCH:?set SCRATCH to the review scratch directory}
LOG=$S/memwatch.log
PER_KB=${PER_KB:-16000000}
FLOOR_KB=${FLOOR_KB:-25000000}
echo "$(date +%T) start per=${PER_KB}KB floor=${FLOOR_KB}KB" >> $LOG
while [ ! -e $S/memwatch.stop ]; do
  avail=$(awk '/MemAvailable/{print $2}' /proc/meminfo)
  big=""; bigrss=0
  for p in /proc/[0-9]*; do
    pid=${p#/proc/}
    rss=$(awk '/VmRSS/{print $2}' $p/status 2>/dev/null) || continue
    [ -z "$rss" ] && continue
    cmd=$(tr '\0' ' ' < $p/cmdline 2>/dev/null)
    case "$cmd" in *scratchpad/rev1*|*/w/meas/qemu/*|*/w/post/*|*qemu-*) ;; *) continue;; esac
    if [ "$rss" -gt "$PER_KB" ]; then
      echo "$(date +%T) KILL per-proc rss=${rss}KB avail=${avail}KB pid=$pid $(echo $cmd | cut -c1-300)" >> $LOG
      kill -9 $pid 2>/dev/null; continue
    fi
    if [ "$rss" -gt "$bigrss" ]; then bigrss=$rss; big=$pid; bigcmd=$cmd; fi
  done
  if [ "$avail" -lt "$FLOOR_KB" ] && [ -n "$big" ]; then
    echo "$(date +%T) KILL floor rss=${bigrss}KB avail=${avail}KB pid=$big $(echo $bigcmd | cut -c1-300)" >> $LOG
    kill -9 $big 2>/dev/null
  fi
  awk -v t="$(date +%T)" '/MemAvailable/{a=$2} END{print t, "avail", a}' /proc/meminfo > $S/memwatch.last
  timeout 3 tail -f /dev/null
done
echo "$(date +%T) stop" >> $LOG
