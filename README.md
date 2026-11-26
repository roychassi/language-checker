# Language Checker

A [HolyArrs](https://holyarrs.com) plugin that turns down releases HolyArrs refuses for a missing audio language, so they are blocklisted for that movie or episode and searched again.

Release names often don't say which languages are inside (`MULTI`, `DUAL`). HolyArrs reads the real audio tracks when it imports a download. If the Version requires an audio language, such as English, Japanese or French, and the tracks don't show it, HolyArrs refuses the file and it waits in **Needs a decision**. This plugin does the waiting-for-a-person part: it turns that release down, and HolyArrs searches again.

Needs HolyArrs 1.3.0 or newer.

## How it works

1. Every minute it reads **Needs a decision**.
2. For an item refused because its audio doesn't have a language the Version requires, it asks HolyArrs to **turn the release down**. HolyArrs blocklists that release for that movie or episode only and searches again, in the same step.
3. It counts the files tried per movie or episode. At **Max tries** it stops, leaves the last file in Needs a decision and tells you.
4. It sends one summary message per check through your HolyArrs alerts.

It reads no media files, deletes nothing, and needs no outside network access. Because HolyArrs refuses the file at import, a file with the wrong audio never enters your library.

## Setup

1. **Require the audio you want.** HolyArrs only refuses a file if the Version requires a language.
   - One title: if it has more than one Version, edit the main Version and enter the codes under **Required audio languages**, such as `en` or `ja`.
   - One or many titles: in the Library, select them, open the **···** menu and choose **Audio languages**. Each movie's or show's main Version gets them. An empty box takes any audio again.
   - New titles: **Settings > Library folders > Audio for new titles**.
2. **Let `MULTI` and `DUAL` releases through your Pick rules** if you want them tried. HolyArrs only warns about a name that shows no language. A Pick rule that must have a word such as `dub` will still turn them away by name.
3. **Add an alert that takes plugin messages** if you want the summaries.
4. **Install the plugin.** It starts in **dry run**: it only alerts about what it would turn down. Check a few alerts, then switch **Dry run** off.

## Settings

| Setting | Default | What it does |
|---|---|---|
| Dry run | on | Only alert. Turn it off to turn releases down. |
| Max tries per movie or episode | 3 | Files tried per movie or episode. The last one stays in Needs a decision. |
| Check every (seconds) | 60 | How often to read Needs a decision. 30 to 3600. |
| Send alerts | on | Send a message through your alerts. |
| Refusal text to match | `does not establish every language required` | Part of HolyArrs' own refusal message. Change it only if HolyArrs rewords that message. |

### Going past the limit

When a movie or episode reaches Max tries, the plugin tells you and leaves the last file in Needs a decision. To try more:

- press **Search** on that item in Needs a decision: the next refused file starts a fresh round of Max tries, or
- raise **Max tries**. Counts are kept per movie or episode, so raising it lets the waiting file be turned down and the search carry on.

## Permissions

| Permission | Why |
|---|---|
| `downloads.decisions.read` | Read Needs a decision. |
| `downloads.decisions.change` | Turn the refused release down, which also searches again. |
| `alerts.webhooks.send` | Send the summary message. |

No hosts, no folders, no secrets. Nothing it asks for shows a red warning at install.

## Limits

- It acts only on the audio-language refusal. Other items in Needs a decision are left alone.
- HolyArrs never counts an audio track with no language tag as a language. A file whose track is untagged is refused for that reason, and so turned down.
- The plugin can't look inside a release before it downloads, so each wrong file is downloaded once.
- HolyArrs lets a plugin send 20 messages an hour. The plugin sends at most 15 and groups lines into one message per check.
- Its memory is one small file, `state.json`, in the plugin's data folder.

## Development

```sh
go vet ./...
go test -race ./...
mkdir -p /tmp/plugin-data
HOLYARRS_PLUGIN_TARGET=127.0.0.1:6262 HOLYARRS_PLUGIN_TOKEN=test HOLYARRS_PLUGIN_DATA=/tmp/plugin-data go run .
```

Standard library only. Built with `CGO_ENABLED=0` for linux amd64, arm64 and armv7.

## Licence

MIT
