#!/usr/bin/env bash
# upgrade-gate.sh proves, before a release is published, that an installed and
# supervised tokenops daemon moves onto the new binary by itself.
#
#   scripts/upgrade-gate.sh launchd   any Mac: an isolated launchd job
#   scripts/upgrade-gate.sh brew      CI runners only: Homebrew installs the
#                                     published cask, then upgrades to the candidate
#   scripts/upgrade-gate.sh systemd   CI runners only: a systemd user unit runs the
#                                     published tarball's binary, which is then
#                                     replaced by the candidate
#
# The old binary is the latest published release, downloaded and checked against
# its checksums.txt. The candidate is built from the working tree with the
# version ldflags goreleaser uses (CANDIDATE_VERSION, default gate-<commit>).
# Nothing is published, tagged or installed outside the gate's own directories,
# except in the brew variant, which runs only where GITHUB_ACTIONS=true.
#
# Isolation for the launchd variant on a Mac: a job whose label contains "test"
# (TOKENOPS_SUPERVISOR_LABEL tells the daemon it is supervised under that label),
# its own HOME and XDG_* directories, its own loopback port and its own log. The
# job never touches de.klarlabs.tokenops, ~/.tokenops, ~/.config/tokenops, port
# 7878 or ~/Library/LaunchAgents.
#
# On a CI runner the job runs under the default label de.klarlabs.tokenops. The
# runner has no operator daemon, and a published release can be supervised only
# under that label: a release from before TOKENOPS_SUPERVISOR_LABEL cannot honour
# any other.
#
# Locally, OLD_BINARY and OLD_VERSION may name the old binary instead of the
# latest release (it must honour TOKENOPS_SUPERVISOR_LABEL).
#
# The systemd variant runs only on CI, and under the unit name the daemon
# itself recognises: tokenops.service (internal/daemon SystemdName). A published
# release finds its supervisor by the cgroup suffix "/tokenops.service" and
# has no way to be told another name, so a test unit under another name would
# never see its old daemon stop. The runner is ephemeral and has no operator
# daemon, which is the same reasoning the launchd variant applies on CI to the
# default label. The unit is rendered by `daemon install --no-load` from the
# old binary, so it is the template the installer writes. Its HOME, XDG_* and
# port come from a drop-in of the same unit.
set -Eeuo pipefail

REPO_SLUG="klarlabs-studio/tokenops"
MODULE="go.klarlabs.de/tokenops"
SWITCH_TIMEOUT=120  # seconds from the swap to a running new version
START_TIMEOUT=180   # seconds for the old version to come up healthy

VARIANT="${1:-}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"

log() { printf '[upgrade-gate] %s\n' "$*" >&2; }
die() { log "FAIL: $*"; exit 1; }

case "$VARIANT" in
launchd | brew | systemd) ;;
*) die "usage: $0 launchd|brew|systemd" ;;
esac
case "$VARIANT" in
launchd | brew)
	[ "$(uname -s)" = Darwin ] || die "the $VARIANT variant runs on macOS (launchd and Homebrew)"
	;;
systemd)
	[ "$(uname -s)" = Linux ] || die "the systemd variant runs on Linux"
	;;
esac
if [ "$VARIANT" = brew ] && [ "${GITHUB_ACTIONS:-}" != true ]; then
	die "the brew variant installs into the Homebrew prefix and runs only on a CI runner"
fi
if [ "$VARIANT" = systemd ] && [ "${GITHUB_ACTIONS:-}" != true ]; then
	die "the systemd variant starts a user manager unit and runs only on a CI runner"
fi

case "$(uname -m)" in
arm64 | aarch64) ARCH=arm64 ;;
x86_64 | amd64) ARCH=amd64 ;;
*) die "unsupported architecture $(uname -m)" ;;
esac
case "$(uname -s)" in
Darwin) OS_NAME=darwin ;;
Linux) OS_NAME=linux ;;
*) die "unsupported operating system $(uname -s)" ;;
esac

WORK="$(mktemp -d "${TMPDIR:-/tmp}/tokenops-upgrade-gate.XXXXXX")"
DOMAIN=""
LABEL=""
LOG="$WORK/daemon.log"
CI_RUN=""
[ "${GITHUB_ACTIONS:-}" = true ] && CI_RUN=1
PLIST="$WORK/job.plist"
BREW_TAP="gate/upgrade"
BREW_CASK="$BREW_TAP/tokenops"

cleanup() {
	local rc=$?
	set +e
	if [ "$rc" -ne 0 ] && [ -s "$LOG" ]; then
		log "last daemon log lines:"
		tail -n 40 "$LOG" >&2
	fi
	if [ -n "$DOMAIN" ] && [ -n "$LABEL" ]; then
		launchctl bootout "$DOMAIN/$LABEL" >/dev/null 2>&1
		for _ in 1 2 3 4 5 6 7 8 9 10; do
			launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1 || break
			sleep 1
		done
		if launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; then
			log "WARNING: job $DOMAIN/$LABEL is still loaded"
		else
			log "job $DOMAIN/$LABEL removed"
		fi
	fi
	if [ "$VARIANT" = brew ]; then
		HOMEBREW_NO_AUTO_UPDATE=1 brew uninstall --cask --force "$BREW_CASK" >/dev/null 2>&1
		brew untap "$BREW_TAP" >/dev/null 2>&1
	fi
	if [ "$VARIANT" = systemd ]; then
		unit_cleanup
	fi
	case "$WORK" in
	"${TMPDIR:-/tmp}"/tokenops-upgrade-gate.*) rm -rf "$WORK" ;;
	esac
	exit "$rc"
}
trap cleanup EXIT


# version_of asks a running daemon which version it serves; empty if it does not answer.
version_of() {
	curl -fsS --max-time 2 "http://127.0.0.1:$PORT/version" 2>/dev/null | jq -r '.version // empty' 2>/dev/null || true
}

# wait_for_version polls until the daemon serves the version $1 or the deadline passes.
wait_for_version() {
	local want="$1" deadline=$(($(date +%s) + $2)) got
	while :; do
		got="$(version_of)"
		[ "$got" = "$want" ] && return 0
		[ "$(date +%s)" -ge "$deadline" ] && { log "daemon serves '${got:-nothing}', want $want"; return 1; }
		sleep 1
	done
}

# job_pid prints the pid of the test job, empty when it runs none.
job_pid() {
	if [ "$VARIANT" = systemd ]; then
		unit_pid
		return
	fi
	launchctl print "$DOMAIN/$LABEL" 2>/dev/null | sed -n 's/^[[:space:]]*pid = //p' | head -n 1
}

# load_job writes the test job's plist and bootstraps it: the gui domain first,
# then the user domain if the gui one refuses. The domain that works is logged.
load_job() {
	local bin_link="$1"
	local override=""
	if [ -n "$CI_RUN" ]; then
		LABEL="de.klarlabs.tokenops"
	else
		LABEL="dev.tokenops.upgrade-gate-test.$$"
		[ -z "${LABEL##*test*}" ] || die "the test label must contain 'test': $LABEL"
		override="    <key>TOKENOPS_SUPERVISOR_LABEL</key>
    <string>$LABEL</string>"
	fi
	PORT="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
	cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$bin_link</string>
    <string>start</string>
  </array>
  <key>KeepAlive</key>
  <true/>
  <key>RunAtLoad</key>
  <true/>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>
    <string>$HOME_DIR</string>
    <key>XDG_CONFIG_HOME</key>
    <string>$HOME_DIR/.config</string>
    <key>XDG_DATA_HOME</key>
    <string>$HOME_DIR/.local/share</string>
    <key>XDG_STATE_HOME</key>
    <string>$HOME_DIR/.local/state</string>
    <key>XDG_CACHE_HOME</key>
    <string>$HOME_DIR/.cache</string>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
$override
    <key>TOKENOPS_LISTEN</key>
    <string>127.0.0.1:$PORT</string>
    <key>TOKENOPS_KEYCHAIN_DISABLED</key>
    <string>1</string>
  </dict>
  <key>StandardOutPath</key>
  <string>$LOG</string>
  <key>StandardErrorPath</key>
  <string>$LOG</string>
  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
EOF
	for DOMAIN in "gui/$(id -u)" "user/$(id -u)"; do
		if launchctl bootstrap "$DOMAIN" "$PLIST" 2>"$WORK/bootstrap.err"; then
			log "job $LABEL bootstrapped in $DOMAIN on port $PORT"
			return 0
		fi
		log "bootstrap in $DOMAIN refused: $(tr -d '\n' <"$WORK/bootstrap.err")"
	done
	DOMAIN=""
	LABEL=""
	die "no launchd domain accepted the test job"
}

# wait_for_log polls the daemon log for a line until the deadline; the line is a fixed string.
wait_for_log() {
	local text="$1" deadline=$(($(date +%s) + $2))
	while :; do
		grep -qF "$text" "$LOG" 2>/dev/null && return 0
		[ "$(date +%s)" -ge "$deadline" ] && return 1
		sleep 1
	done
}

# fetch_old downloads the latest published release for this runner, checks its
# checksum, and leaves the binary at $WORK/old/tokenops.
fetch_old() {
	local tag archive base
	if [ -n "${OLD_BINARY:-}" ]; then
		[ -n "${OLD_VERSION:-}" ] || die "OLD_BINARY needs OLD_VERSION"
		OLD="$OLD_VERSION"
		mkdir -p "$WORK/old"
		cp "$OLD_BINARY" "$WORK/old/tokenops"
		log "old binary: $OLD from OLD_BINARY"
		return 0
	fi
	if [ -n "${GITHUB_TOKEN:-}" ]; then
		tag="$(curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "https://api.github.com/repos/$REPO_SLUG/releases/latest" | jq -r '.tag_name')"
	else
		tag="$(curl -fsSL "https://api.github.com/repos/$REPO_SLUG/releases/latest" | jq -r '.tag_name')"
	fi
	[ -n "$tag" ] && [ "$tag" != null ] || die "could not name the latest release"
	OLD="${tag#v}"
	base="https://github.com/$REPO_SLUG/releases/download/$tag"
	archive="tokenops_${OLD}_${OS_NAME}_${ARCH}.tar.gz"
	mkdir -p "$WORK/old"
	curl -fsSL -o "$WORK/old/checksums.txt" "$base/checksums.txt"
	curl -fsSL -o "$WORK/old/$archive" "$base/$archive"
	(cd "$WORK/old" && grep -F "  $archive" checksums.txt | shasum -a 256 -c - >/dev/null) || die "checksum of $archive does not match checksums.txt"
	tar -xzf "$WORK/old/$archive" -C "$WORK/old" tokenops
	log "old release: $OLD ($archive, checksum verified)"
}

# build_candidate builds tokenops and tokenopsd from the working tree with the
# version ldflags goreleaser uses, into $WORK/candidate.
build_candidate() {
	local commit date ldflags
	CAND="${CANDIDATE_VERSION:-gate-$(git -C "$REPO" rev-parse --short HEAD)}"
	CAND="${CAND#v}"
	[ "$CAND" != "$OLD" ] || die "the candidate is the old release ($CAND)"
	commit="$(git -C "$REPO" rev-parse --short HEAD 2>/dev/null || echo unknown)"
	date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	ldflags="-s -w -X $MODULE/internal/version.Version=$CAND -X $MODULE/internal/version.Commit=$commit -X $MODULE/internal/version.Date=$date"
	mkdir -p "$WORK/candidate"
	(cd "$REPO" && go build -trimpath -ldflags "$ldflags" -o "$WORK/candidate/tokenops" ./cmd/tokenops \
		&& go build -trimpath -ldflags "$ldflags" -o "$WORK/candidate/tokenopsd" ./cmd/tokenopsd)
	log "candidate: $CAND built from $commit"
}

# assert_switch waits for the old daemon to stop itself onto the candidate, and
# checks that the supervisor started the candidate and the API reports it.
assert_switch() {
	local old_pid="$1" deadline=$(($(date +%s) + SWITCH_TIMEOUT)) pid
	wait_for_log "tokenops was replaced by an install; stopping so the supervisor starts the installed binary" "$SWITCH_TIMEOUT" \
		|| die "the old daemon did not log the replacement within ${SWITCH_TIMEOUT}s (a binary that predates TOKENOPS_SUPERVISOR_LABEL cannot be supervised under a test label)"
	log "old daemon noticed the install and stopped"
	while :; do
		pid="$(job_pid)"
		[ -n "$pid" ] && [ "$pid" != "$old_pid" ] && break
		[ "$(date +%s)" -ge "$deadline" ] && die "the supervisor did not start a new daemon (pid still ${pid:-none})"
		sleep 1
	done
	log "supervisor started pid $pid in place of $old_pid"
	wait_for_version "$CAND" $((deadline - $(date +%s))) || die "the API does not report $CAND within ${SWITCH_TIMEOUT}s"
	if kill -0 "$old_pid" 2>/dev/null; then die "old daemon pid $old_pid is still running"; fi
	log "PASS: $OLD -> $CAND: the daemon moved onto the new binary by itself"
}

# systemd_unit is the unit the gate runs: the one the daemon's supervisor check
# recognises (see the header).
SYSTEMD_UNIT="tokenops.service"
UNIT_LINK=""
UNIT_DROPIN=""

# unit_pid prints the main pid of the systemd unit, empty when it runs none.
unit_pid() {
	local pid
	pid="$(systemctl --user show -p MainPID --value "$SYSTEMD_UNIT" 2>/dev/null || true)"
	[ "$pid" = 0 ] || printf '%s\n' "$pid"
}

# ensure_user_manager makes sure a systemd user manager runs for this user and
# that systemctl --user can reach it. A runner whose user has no session has none
# until it lingers, so lingering is enabled and the manager started.
ensure_user_manager() {
	local uid
	uid="$(id -u)"
	export XDG_RUNTIME_DIR="/run/user/$uid"
	if systemctl --user show-environment >/dev/null 2>&1; then
		log "systemd user manager is running for $(id -un)"
		return 0
	fi
	log "no systemd user manager for $(id -un); enabling lingering and starting it"
	sudo -n loginctl enable-linger "$(id -un)" || log "loginctl enable-linger failed"
	sudo -n systemctl start "user@$(id -u).service" || log "start of user@$(id -u).service failed"
	local deadline=$(($(date +%s) + 60))
	until systemctl --user show-environment >/dev/null 2>&1; do
		[ "$(date +%s)" -ge "$deadline" ] && return 1
		sleep 1
	done
	log "systemd user manager is running for $(id -un) after lingering"
}

# unit_cleanup stops the test unit and removes the unit file and drop-in this
# gate linked into the user manager's search path. Only what this run created is
# removed: a link that points elsewhere is left alone.
unit_cleanup() {
	systemctl --user stop "$SYSTEMD_UNIT" >/dev/null 2>&1
	if [ -n "$UNIT_LINK" ] && [ -L "$UNIT_LINK" ] && [ "$(readlink "$UNIT_LINK")" = "$WORK/home/.config/systemd/user/$SYSTEMD_UNIT" ]; then
		rm -f "$UNIT_LINK"
		log "unit link $UNIT_LINK removed"
	fi
	if [ -n "$UNIT_DROPIN" ] && [ -f "$UNIT_DROPIN" ]; then
		rm -f "$UNIT_DROPIN"
		rmdir "$(dirname "$UNIT_DROPIN")" 2>/dev/null || true
	fi
	systemctl --user daemon-reload >/dev/null 2>&1
}

# run_systemd: the published tarball's binary is installed into a bin directory
# and supervised by a user unit rendered by its own `daemon install --no-load`.
# The candidate then replaces the binary the way the tarball installer does: a
# new file renamed over the old path.
run_systemd() {
	local install_dir="$WORK/install/bin" runner_unit_dir old_pid
	HOME_DIR="$WORK/home"
	mkdir -p "$HOME_DIR"
	fetch_old
	build_candidate

	mkdir -p "$install_dir"
	cp "$WORK/old/tokenops" "$install_dir/tokenops"
	chmod 755 "$install_dir/tokenops"

	# The unit is rendered by the old binary's own installer. HOME and every XDG
	# directory point into WORK, so the render and the daemon's state stay there.
	env HOME="$HOME_DIR" XDG_CONFIG_HOME="$HOME_DIR/.config" XDG_DATA_HOME="$HOME_DIR/.local/share" \
		XDG_STATE_HOME="$HOME_DIR/.local/state" XDG_CACHE_HOME="$HOME_DIR/.cache" \
		"$install_dir/tokenops" daemon install --no-load >"$WORK/install.out" 2>&1 \
		|| { cat "$WORK/install.out" >&2; die "daemon install --no-load failed for $OLD"; }
	local rendered="$HOME_DIR/.config/systemd/user/$SYSTEMD_UNIT"
	[ -f "$rendered" ] || die "daemon install did not write $rendered"
	grep -qF "ExecStart=$install_dir/tokenops start" "$rendered" \
		|| die "the rendered unit does not start $install_dir/tokenops"

	# The gate loads the unit into the runner's own user manager, which reads
	# unit files only from its search path, and carries the test's HOME, XDG_*,
	# port and log in a drop-in of the same unit.
	ensure_user_manager || die "no systemd user manager could be reached for $(id -un)"
	runner_unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
	mkdir -p "$runner_unit_dir"
	[ ! -e "$runner_unit_dir/$SYSTEMD_UNIT" ] || die "$runner_unit_dir/$SYSTEMD_UNIT already exists on the runner"
	UNIT_LINK="$runner_unit_dir/$SYSTEMD_UNIT"
	UNIT_DROPIN="$runner_unit_dir/$SYSTEMD_UNIT.d/upgrade-gate.conf"
	PORT="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
	ln -s "$rendered" "$UNIT_LINK"
	mkdir -p "$(dirname "$UNIT_DROPIN")"
	cat >"$UNIT_DROPIN" <<EOF
[Service]
Environment="XDG_CONFIG_HOME=$HOME_DIR/.config" "XDG_DATA_HOME=$HOME_DIR/.local/share" "XDG_STATE_HOME=$HOME_DIR/.local/state" "XDG_CACHE_HOME=$HOME_DIR/.cache"
Environment="TOKENOPS_LISTEN=127.0.0.1:$PORT" "TOKENOPS_KEYCHAIN_DISABLED=1"
StandardOutput=append:$LOG
EOF
	systemctl --user daemon-reload || die "systemctl --user daemon-reload failed"
	systemctl --user cat "$SYSTEMD_UNIT" | grep -qF "ExecStart=$install_dir/tokenops start" \
		|| die "the user manager does not load $SYSTEMD_UNIT from the rendered unit"

	systemctl --user start "$SYSTEMD_UNIT" || die "systemctl --user start $SYSTEMD_UNIT failed"
	wait_for_version "$OLD" "$START_TIMEOUT" || die "the old $OLD daemon did not come up healthy"
	old_pid="$(unit_pid)"
	[ -n "$old_pid" ] || die "systemd reports no main pid for the old daemon"
	log "old daemon $OLD healthy as pid $old_pid under $SYSTEMD_UNIT"

	# The install: the candidate lands beside the old binary and is renamed over
	# it, so the path never names a half-written file.
	cp "$WORK/candidate/tokenops" "$install_dir/.tokenops.new"
	chmod 755 "$install_dir/.tokenops.new"
	mv -f "$install_dir/.tokenops.new" "$install_dir/tokenops"
	log "installed $CAND over $OLD"

	assert_switch "$old_pid"
}

run_launchd() {
	local prefix="$WORK/prefix" cask_old cask_new
	HOME_DIR="$WORK/home"
	mkdir -p "$HOME_DIR"
	fetch_old
	build_candidate

	# Lay out the install the way Homebrew does: a versioned Caskroom directory
	# and bin/tokenops pointing into it.
	cask_old="$prefix/Caskroom/tokenops/$OLD"
	cask_new="$prefix/Caskroom/tokenops/$CAND"
	mkdir -p "$cask_old" "$prefix/bin"
	cp "$WORK/old/tokenops" "$cask_old/tokenops"
	ln -s "$cask_old/tokenops" "$prefix/bin/tokenops"

	load_job "$prefix/bin/tokenops"
	wait_for_version "$OLD" "$START_TIMEOUT" || die "the old $OLD daemon did not come up healthy"
	local old_pid
	old_pid="$(job_pid)"
	[ -n "$old_pid" ] || die "launchd reports no pid for the old daemon"
	log "old daemon $OLD healthy as pid $old_pid"

	# The install: the candidate lands, bin/tokenops is repointed, and the old
	# Caskroom directory is deleted, as brew upgrade does.
	mkdir -p "$cask_new"
	cp "$WORK/candidate/tokenops" "$cask_new/tokenops"
	ln -s "$cask_new/tokenops" "$prefix/bin/tokenops.next"
	mv -f "$prefix/bin/tokenops.next" "$prefix/bin/tokenops"
	rm -rf "$cask_old"
	log "installed $CAND over $OLD"

	assert_switch "$old_pid"
}

# fetch_published_cask writes the published Homebrew cask into the tap, as is,
# and checks that it names the old release.
fetch_published_cask() {
	local cask="$WORK/tap/Casks/tokenops.rb"
	mkdir -p "$WORK/tap/Casks"
	curl -fsSL -o "$cask" "https://raw.githubusercontent.com/klarlabs-studio/homebrew-tap/main/Casks/tokenops.rb" \
		|| die "could not fetch the published cask"
	grep -qF "version \"$OLD\"" "$cask" || die "the published cask is not at $OLD"
}

# candidate_cask makes the candidate's cask from the published one: the same
# text, with the version, the arm64 checksum and the arm64 download pointed at
# the candidate archive that build_candidate packs. Done in Python, not sed:
# BSD sed has no 0,/re/ range, and a silent no-op here once shipped a stale
# checksum.
candidate_cask() {
	local archive="$WORK/candidate/tokenops_${CAND}_darwin_arm64.tar.gz"
	tar -czf "$archive" -C "$WORK/candidate" tokenops tokenopsd
	python3 - "$WORK/tap/Casks/tokenops.rb" "$WORK/candidate.rb" "$OLD" "$CAND" "$archive" "$WORK/candidate" "$REPO_SLUG" <<'PY' \
		|| die "could not write the candidate cask"
import hashlib, re, sys
src, dst, old, new, archive, candidate_dir, slug = sys.argv[1:8]
text = open(src).read()
digest = hashlib.sha256(open(archive, "rb").read()).hexdigest()
text, n_version = re.subn(r'version "%s"' % re.escape(old), 'version "%s"' % new, text, count=1)
arm = re.search(r'on_arm do\n\s+sha256 "[0-9a-f]+"\n\s+url "[^"]*"', text)
if not (n_version == 1 and arm):
    raise SystemExit("published cask does not have the expected version or arm64 block")
block = arm.group(0)
block = re.sub(r'sha256 "[0-9a-f]+"', 'sha256 "%s"' % digest, block, count=1)
block = re.sub(r'url "[^"]*"', 'url "file://%s/tokenops_#{version}_darwin_arm64.tar.gz"' % candidate_dir, block, count=1)
text = text[:arm.start()] + block + text[arm.end():]
open(dst, "w").write(text)
PY
	grep -qF "file://$WORK/candidate/tokenops_#{version}_darwin_arm64.tar.gz" "$WORK/candidate.rb" \
		|| die "the candidate cask does not point at its archive"
	grep -qF "$(shasum -a 256 "$archive" | awk '{print $1}')" "$WORK/candidate.rb" \
		|| die "the candidate cask does not carry the archive's checksum"
}

commit_tap() {
	git -C "$WORK/tap" init -q 2>/dev/null || true
	git -C "$WORK/tap" add -A
	git -C "$WORK/tap" -c user.name=upgrade-gate -c user.email=upgrade-gate@localhost commit -q -m "$1"
}

run_brew() {
	local prefix bin_link cask_dir old_bin
	fetch_old
	build_candidate
	fetch_published_cask
	commit_tap "published cask $OLD"

	# The old cask is the published one, installed through a local tap so the
	# upgrade below can come from a second local tap commit.
	HOMEBREW_NO_AUTO_UPDATE=1 brew tap "$BREW_TAP" "$WORK/tap" >/dev/null
	HOMEBREW_NO_AUTO_UPDATE=1 brew install --cask "$BREW_CASK" >/dev/null
	prefix="$(brew --prefix)"
	bin_link="$prefix/bin/tokenops"
	cask_dir="$prefix/Caskroom/tokenops"
	old_bin="$(readlink "$bin_link")"
	case "$old_bin" in
	*"/Caskroom/tokenops/$OLD/tokenops") ;;
	*) die "brew installed $old_bin, not the $OLD binary" ;;
	esac
	if xattr -p com.apple.quarantine "$old_bin" >/dev/null 2>&1; then
		die "the installed $OLD binary is quarantined"
	fi
	HOME_DIR="$WORK/home"
	mkdir -p "$HOME_DIR"

	load_job "$bin_link"
	wait_for_version "$OLD" "$START_TIMEOUT" || die "the old $OLD daemon did not come up healthy"
	local old_pid
	old_pid="$(job_pid)"
	[ -n "$old_pid" ] || die "launchd reports no pid for the old daemon"
	log "old daemon $OLD healthy as pid $old_pid, installed by brew"

	candidate_cask
	cp "$WORK/candidate.rb" "$WORK/tap/Casks/tokenops.rb"
	commit_tap "candidate cask $CAND"
	git -C "$(brew --repo "$BREW_TAP")" pull -q "$WORK/tap"
	HOMEBREW_NO_AUTO_UPDATE=1 brew upgrade --cask "$BREW_CASK" >/dev/null

	[ ! -e "$cask_dir/$OLD" ] || die "brew upgrade left the $OLD directory in the Caskroom"
	[ "$(readlink "$bin_link")" = "$cask_dir/$CAND/tokenops" ] || die "bin/tokenops does not point at the $CAND binary"
	if xattr -p com.apple.quarantine "$cask_dir/$CAND/tokenops" >/dev/null 2>&1; then
		die "the upgraded $CAND binary is quarantined"
	fi
	log "brew upgraded the cask to $CAND; the old Caskroom directory is gone"

	assert_switch "$old_pid"
}

case "$VARIANT" in
launchd) run_launchd ;;
brew) run_brew ;;
systemd) run_systemd ;;
esac
