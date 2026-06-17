#!/bin/zsh
set -e
PROJECT="$HOME/Projects/filemaid"
CONFIG_DIR="$HOME/.config/filemaid"
DATA_DIR="$HOME/.local/share/filemaid"
REVIEW_DIR="$HOME/.filemaid/review"
BIN_DIR="$HOME/.local/bin"
LAUNCHD_DIR="$HOME/Library/LaunchAgents"
SCAN="biz.logicminds.filemaid.scan"
CLEANUP="biz.logicminds.filemaid.cleanup"

mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$REVIEW_DIR" "$BIN_DIR" "$LAUNCHD_DIR"

if [[ ! -f "$CONFIG_DIR/config.json" ]]; then
    cp "$PROJECT/config.json" "$CONFIG_DIR/config.json"
fi

cat > "$BIN_DIR/filemaid" <<'EOF'
#!/bin/zsh
export PATH="/usr/local/bin:/opt/homebrew/bin:$PATH"
export PYTHONPATH="$HOME/Projects/filemaid${PYTHONPATH:+:$PYTHONPATH}"
exec /usr/bin/python3 -m filemaid "$@"
EOF
chmod +x "$BIN_DIR/filemaid"

cp "$PROJECT/biz.logicminds.filemaid.scan.plist" "$LAUNCHD_DIR/"
cp "$PROJECT/biz.logicminds.filemaid.cleanup.plist" "$LAUNCHD_DIR/"

# Boot out any existing agents first to make install idempotent
for label in "$SCAN" "$CLEANUP"; do
    launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1 && launchctl bootout "gui/$(id -u)/$label" || true
done

launchctl bootstrap "gui/$(id -u)" "$LAUNCHD_DIR/biz.logicminds.filemaid.scan.plist"
launchctl bootstrap "gui/$(id -u)" "$LAUNCHD_DIR/biz.logicminds.filemaid.cleanup.plist"

echo "filemaid installed"
echo "NOTE: If the scan agent cannot read Desktop/Downloads, grant Full Disk Access to /usr/bin/python3 in System Settings -> Privacy & Security -> Full Disk Access, or rely on Shortcuts folder automations instead."
