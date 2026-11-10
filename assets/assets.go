// Package assets provides the prompts, the speaker roster and the HTML templates as embedded resources.
package assets

import "embed"

// promptDir is where the prompts live. The file name is the mode name
// (prompts/tech_duet.md → mode="tech_duet"). The prefix is the genre, so that the listing
// groups modes by genre (the input type lives in the front matter).
const promptDir = "prompts"

// PromptFiles is the set of embedded prompt files.
//
//go:embed prompts/*.md
var PromptFiles embed.FS

// SpeakersJSON is the VOICEVOX engine's /speakers response saved as is.
//
// Which speakers to use is the app's policy, so it lives here rather than in go-voicevox.
// The library only interprets the response's structure and ships no roster.
//
// Updating it is just fetching it again and replacing the file. Nothing is copied by hand, so no style
// the engine adds can be missed. It is saved pretty-printed so that the diff on an engine update is readable.
//
//	curl -s "$VOICEVOX_API_URL/speakers" |
//	  python3 -c 'import json,sys; json.dump(json.load(sys.stdin), sys.stdout, ensure_ascii=False, indent=2)' \
//	  > assets/speakers.json
//
// Only the vocabulary (who has which style) is read from here. Style IDs are not used —
// they change between engine builds, so go-voicevox asks the real engine at startup.
//
//go:embed speakers.json
var SpeakersJSON []byte

// Templates are the web side's HTML templates.
//
//go:embed templates/*.html templates/partials/*.html
var Templates embed.FS

// StaticFiles are the web side's static files. The directory is embedded as a whole,
// so adding a file is enough for it to be served.
//
//go:embed static
var StaticFiles embed.FS
