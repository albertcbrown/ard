#!/bin/sh
# Fake agent used by the executor tests. Usage: fakeagent.sh <mode>
case "$1" in
ok)     echo out; echo err >&2; echo done > result.txt; exit 0 ;;
fail)   echo failing >&2; exit 3 ;;
crash)  kill -9 $$ ;;
hang)   sleep 300 & echo $! > "$ARD_WORK/../grandchild.$ARD_JOB"; wait ;;
stubborn) trap '' TERM; sleep 300 & wait ;;
env)    env ;;
talk)   i=0; while [ $i -lt 30 ]; do echo tick $i; i=$((i+1)); sleep 0.1; done ;;
write)  echo x > probe.txt ;;
*)      exit 99 ;;
esac
