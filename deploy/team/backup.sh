#!/bin/sh
# Nightly pg_dump of the team plane into /backups, keeping BACKUP_KEEP_DAYS
# days. Runs as the compose service "backup"; `docker compose exec backup
# sh /usr/local/bin/backup.sh once` takes one now.
set -eu

keep="${BACKUP_KEEP_DAYS:-14}"

dump() {
	stamp=$(date -u +%Y%m%dT%H%M%SZ)
	tmp="/backups/.tokenops-$stamp.dump.partial"
	pg_dump --format=custom --compress=6 --file="$tmp"
	mv "$tmp" "/backups/tokenops-$stamp.dump"
	find /backups -name 'tokenops-*.dump' -type f -mtime +"$keep" -delete
	echo "backup: wrote tokenops-$stamp.dump"
}

if [ "${1:-}" = "once" ]; then
	dump
	exit 0
fi

while true; do
	dump || echo "backup: pg_dump failed; retrying in an hour" >&2
	# Next run in 24 hours, or an hour after a failure.
	if [ -n "$(find /backups -name 'tokenops-*.dump' -mmin -60 2>/dev/null)" ]; then
		sleep 86400
	else
		sleep 3600
	fi
done
