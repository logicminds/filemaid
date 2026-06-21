# filemaid FAQ

## General

### What is filemaid?

filemaid is a local, macOS-only file organizer. It classifies files dropped on `~/Desktop` or in `~/Downloads` using a local Ollama LLM, moves them into categorized archive folders, applies Finder tags, writes the classification reason as a Finder comment, and quarantines uncertain items for review. It also builds a `~/Documents/Filemaid` hub with Smart Folders, Archive/Review aliases, and a Finder sidebar pin, and cleans up stale development artifacts on a schedule.

## Finder Tags and Comments

### Why do my files have Finder tags but no Finder comments?

filemaid writes both tags and comments into the file's extended attributes (xattrs):

- Tags: `com.apple.metadata:_kMDItemUserTags`
- Comments: `com.apple.metadata:kMDItemFinderComment`

Finder tags are read directly from the xattr, so they appear immediately. Finder comments, however, are displayed through **Spotlight indexing**. If Spotlight has not indexed the archive folder, the comment field in Finder's Get Info panel will remain empty even though the comment data is on the file.

You can verify the comment xattr is present with:

```bash
xattr -l ~/Documents/Archive/Category/SomeFile.png | grep kMDItemFinderComment
```

If it prints a `bplist00` value, filemaid wrote the comment correctly.

### How do I fix missing Finder comments?

Rebuild the Spotlight index for your volume:

```bash
mdutil -s /
sudo mdutil -i on /
sudo mdutil -E /
```

Rebuilding can take anywhere from a few minutes to several hours depending on disk size. After it completes, Finder will display comments and Smart Folders will populate.

### Will tags and comments survive if I move files to another Mac?

Yes, as long as the transfer method preserves extended attributes:

- Preserved: AirDrop, iCloud Drive, Finder copies on APFS/HFS+, Time Machine
- Often stripped: email attachments, Dropbox, Google Drive, FAT32/exFAT drives, most NAS shares, some compressed archives

### Do tags merge with existing tags?

Yes. As of the latest version, filemaid merges new tags with existing tags on each processed file. Tags are deduplicated case-insensitively, and the most recently seen casing wins.

### Do comments append to existing comments?

Yes. filemaid appends new classification reasons to existing Finder comments, separated by `; `. Exact duplicates and case-insensitive matches are skipped.

## Smart Folders

### Why are my Smart Folders empty?

Smart Folders rely on Spotlight-indexed Finder tags. Empty Smart Folders usually mean one of two things:

1. **Missing `filemaid` tag.** Smart Folders search for `kMDItemUserTags == "filemaid"cd`. Files processed before Smart Folders was enabled, or with tags disabled, will not appear.
2. **Spotlight indexing failure.** If `mdls` returns "could not find" for files in `~/Documents/Archive`, Spotlight is not indexing that location.

Fix by rebuilding the Spotlight index:

```bash
sudo mdutil -E /
```

Then regenerate Smart Folders:

```bash
filemaid smart-folders
```

### Where is the Filemaid hub?

The hub is created at `~/Documents/Filemaid` by default. You can change the location with `smart_folders_dir` in `~/.config/filemaid/config.json`. The hub contains:

- One Smart Folder per configured category and per unique Finder tag.
- Finder aliases named `Archive` and `Review`.
- A pinned favorite in Finder's sidebar (added on first build).

To regenerate the hub manually:

```bash
filemaid smart-folders
```

To remove the sidebar pin, open Finder → Settings → Sidebar and uncheck `Filemaid`.

## Processing and Scanning

### How do I process files manually?

```bash
~/.local/bin/filemaid process ~/Desktop/foo.png ~/Downloads/bar.pdf
```

The default output is a human-readable list. Use `--format table` or `--json` for other formats.

### How do I run a dry run?

```bash
~/.local/bin/filemaid process --dry-run <paths>
~/.local/bin/filemaid scan --dry-run
```

### How do I enable smart rename for one run?

```bash
# Use rename_level from config
~/.local/bin/filemaid process --rename <paths>

# Override the threshold for this run (1=most aggressive, 5=most conservative)
~/.local/bin/filemaid process --rename=3 <paths>
```

### What does --force do?

`--force` tells filemaid to process a file even if it looks like a duplicate or is similar to something already in history. Duplicates and similar files are normally routed to review to avoid accidental overwrites or data loss.

### Why did nothing happen during a scan?

The scan command only processes files matching configured rules and age thresholds. If a file was recently processed or does not match any rule, it will be skipped. Check the logs:

```bash
~/.local/bin/filemaid logs --tail 50
```

## Setup and Permissions

### Does filemaid need Full Disk Access?

Only the optional background scan LaunchAgent typically needs Full Disk Access to read `~/Desktop` and `~/Downloads`. The Shortcuts folder-automation path does not require it.

### How do I install or reinstall filemaid?

```bash
make build
./bin/filemaid setup
```

To also install background agents:

```bash
./bin/filemaid setup --agents
```

To use Shortcuts automation instead:

```bash
./bin/filemaid setup --shortcuts
```

## Troubleshooting

### Where are the logs?

```bash
~/.local/bin/filemaid logs --tail 50
```

Logs are stored in `~/.local/share/filemaid/filemaid.log` by default.

### How do I check the current configuration?

```bash
~/.local/bin/filemaid config
```

Configuration is loaded from `~/.config/filemaid/config.json` and merged with defaults.
