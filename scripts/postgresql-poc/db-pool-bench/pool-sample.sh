#!/bin/bash
# usage: pool-sample.sh outfile ; samples connections per pool database every 5s
admin=$(head -1 ~/.config/juju-pg-poc/db-pool)
while :; do
  echo "$(date +%s) load=$(cut -d' ' -f1 /proc/loadavg) $(psql "$admin" -Atc "select string_agg(datname||'='||n, ' ' order by datname) from (select datname, count(*) n from pg_stat_activity where datname like 'juju-0%' group by 1) s" 2>&1)" >> "$1"
  sleep 5
done
