#!/bin/zsh
LAUNCHD_DIR="$HOME/Library/LaunchAgents"
SCAN="biz.logicminds.filemaid.scan"
CLEANUP="biz.logicminds.filemaid.cleanup"
for label in "$SCAN" "$CLEANUP"; do
    launchctl print "gui/$(id -u)/$label" >/dev/null 2>&1 && launchctl bootout "gui/$(id -u)/$label" || true
done
rm -f "$LAUNCHD_DIR"/biz.logicminds.filemaid.*.plist "$HOME/.local/bin/filemaid"
echo "filemaid uninstalled"
