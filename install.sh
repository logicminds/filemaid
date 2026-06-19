#!/bin/zsh
set -e

INSTALL_SCAN=true

usage() {
    cat <<EOF
Usage: ./install.sh [options]

Options:
  --no-scan    Do not install the scheduled scan LaunchAgent.
               filemaid scan can still be run manually.
  -h, --help   Show this help message.
EOF
    exit 0
}

for arg in "$@"; do
    case "$arg" in
        --no-scan) INSTALL_SCAN=false ;;
        -h|--help) usage ;;
        *) echo "Unknown option: $arg" >&2; usage ;;
    esac
done

if [[ -t 0 && "$INSTALL_SCAN" == true ]]; then
    printf "Install the scheduled scan LaunchAgent? [Y/n]: "
    read -r REPLY
    case "$REPLY" in
        [Nn]*) INSTALL_SCAN=false ;;
        *) INSTALL_SCAN=true ;;
    esac
fi

PROJECT="$HOME/Projects/filemaid"
CONFIG_DIR="$HOME/.config/filemaid"
DATA_DIR="$HOME/.local/share/filemaid"
REVIEW_DIR="$HOME/.filemaid/review"
BIN_DIR="$HOME/.local/bin"
LAUNCHD_DIR="$HOME/Library/LaunchAgents"
SCAN="biz.logicminds.filemaid.scan"
CLEANUP="biz.logicminds.filemaid.cleanup"

PYTHON=""
MIN_PY="3.12"
for cand in /opt/homebrew/bin/python3.14 /opt/homebrew/bin/python3.13 /opt/homebrew/bin/python3.12 /usr/local/bin/python3.14 /usr/local/bin/python3.13 /usr/local/bin/python3.12 python3.14 python3.13 python3.12 python3; do
    if command -v "$cand" >/dev/null 2>&1; then
        ver=$("$cand" --version 2>&1 | awk '{print $2}')
        major=$(echo "$ver" | cut -d. -f1)
        minor=$(echo "$ver" | cut -d. -f2)
        if (( major > 3 || (major == 3 && minor >= 12) )); then
            PYTHON="$cand"
            break
        fi
    fi
done

if [[ -z "$PYTHON" ]]; then
    echo "Error: filemaid requires Python $MIN_PY or newer." >&2
    echo "Install it with Homebrew:" >&2
    echo "  brew install python@3.12" >&2
    echo "or" >&2
    echo "  brew install python@3.13" >&2
    echo "Then re-run ./install.sh" >&2
    exit 1
fi

echo "Using Python $ver ($PYTHON)"


mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$REVIEW_DIR" "$BIN_DIR" "$LAUNCHD_DIR"

# Detect total system memory (bytes) and recommend a model.
# Thresholds: >= 24 GB -> 26b, >= 16 GB -> 12b, else metadata-only 7b.
RAM_BYTES=$(sysctl -n hw.memsize)
RAM_GB=$(( RAM_BYTES / 1024 / 1024 / 1024 ))
if (( RAM_GB >= 24 )); then
    RECOMMENDED_MODEL="filemaid-gemma4-26b"
elif (( RAM_GB >= 16 )); then
    RECOMMENDED_MODEL="filemaid-gemma4-12b"
else
    RECOMMENDED_MODEL="filemaid-metadata"
fi

echo "Detected $RAM_GB GB RAM. Recommended model: $RECOMMENDED_MODEL"

MODEL=""
if [[ -t 0 ]]; then
    echo "Which model should filemaid use?"
    echo "  1) filemaid-gemma4-26b  (best quality, needs ~24 GB RAM)"
    echo "  2) filemaid-gemma4-12b  (good quality, needs ~16 GB RAM)"
    echo "  3) filemaid-metadata    (fastest, filename-only, any RAM)"
    printf "Choose [1-3, default %s]: " "$RECOMMENDED_MODEL"
    read -r CHOICE
    case "$CHOICE" in
        1) MODEL="filemaid-gemma4-26b" ;;
        2) MODEL="filemaid-gemma4-12b" ;;
        3) MODEL="filemaid-metadata" ;;
        *) MODEL="$RECOMMENDED_MODEL" ;;
    esac
else
    MODEL="$RECOMMENDED_MODEL"
    echo "Non-interactive install; using recommended model: $MODEL"
fi

echo "Selected model: $MODEL"

if [[ ! -f "$CONFIG_DIR/config.json" ]]; then
    cp "$PROJECT/config.json" "$CONFIG_DIR/config.json"
    # Update the installed config with the selected model.
    "$PYTHON" -c "
import json
p = '$CONFIG_DIR/config.json'
with open(p, 'r') as f:
    cfg = json.load(f)
cfg['model'] = '$MODEL'
with open(p, 'w') as f:
    json.dump(cfg, f, indent=2)
    f.write('\n')
"
fi

cat > "$BIN_DIR/filemaid" <<EOF
#!/bin/zsh
export PATH="/usr/local/bin:/opt/homebrew/bin:\$PATH"
export PYTHONPATH="\$HOME/Projects/filemaid\${PYTHONPATH:+:\$PYTHONPATH}"
exec "$PYTHON" -m filemaid "\$@"
EOF
chmod +x "$BIN_DIR/filemaid"

cp "$PROJECT/biz.logicminds.filemaid.cleanup.plist" "$LAUNCHD_DIR/"
if [[ "$INSTALL_SCAN" == true ]]; then
    cp "$PROJECT/biz.logicminds.filemaid.scan.plist" "$LAUNCHD_DIR/"
fi

# Create custom Ollama models if Ollama is installed
if command -v ollama >/dev/null 2>&1; then
    echo "Creating filemaid Ollama models..."
    ollama create -f "$PROJECT/modelfiles/Modelfile.filemaid-gemma4-26b" filemaid-gemma4-26b || true
    ollama create -f "$PROJECT/modelfiles/Modelfile.filemaid-gemma4-12b" filemaid-gemma4-12b || true
    ollama create -f "$PROJECT/modelfiles/Modelfile.filemaid-metadata" filemaid-metadata || true
else
    echo "Ollama not found; skipping model creation. Install Ollama, then run:"
    echo "  ollama create -f $PROJECT/modelfiles/Modelfile.filemaid-gemma4-26b filemaid-gemma4-26b"
    echo "  ollama create -f $PROJECT/modelfiles/Modelfile.filemaid-gemma4-12b filemaid-gemma4-12b"
    echo "  ollama create -f $PROJECT/modelfiles/Modelfile.filemaid-metadata filemaid-metadata"
fi

# Boot out any existing agents first to make install idempotent
for label in "$SCAN" "$CLEANUP"; do
    launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1 && launchctl bootout "gui/$(id -u)/$label" || true
done

if [[ "$INSTALL_SCAN" != true ]]; then
    rm -f "$LAUNCHD_DIR/biz.logicminds.filemaid.scan.plist"
fi

launchctl bootstrap "gui/$(id -u)" "$LAUNCHD_DIR/biz.logicminds.filemaid.cleanup.plist"
if [[ "$INSTALL_SCAN" == true ]]; then
    launchctl bootstrap "gui/$(id -u)" "$LAUNCHD_DIR/biz.logicminds.filemaid.scan.plist"
fi

if [[ "$INSTALL_SCAN" == true ]]; then
    echo "filemaid installed (periodic scan agent enabled)"
    echo "NOTE: If the scan agent cannot read Desktop/Downloads, grant Full Disk Access to $PYTHON in System Settings -> Privacy & Security -> Full Disk Access, or rely on Shortcuts folder automations instead."
else
    echo "filemaid installed (periodic scan agent disabled; run 'filemaid scan' manually)"
fi
