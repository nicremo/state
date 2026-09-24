# Source transcript: AI-managed Notes for State

The requested State extension is a dead-simple notes function that appears as a
bottom navigation item. Notes stay in one flat list with a title and short
summary. There are no categories or filtering systems beyond normal search.

The creation action is a large `+` button with exactly three choices:

- **Text:** a note editor with the familiar iPhone Notes formatting experience.
- **Photo:** take or choose images, including handwritten notebook pages. Store
  the images and have AI turn them into usable State notes with existing-note
  context.
- **Audio:** record speech and transcribe it before the note is organized.

The notes are AI-managed through OpenRouter. The intended notes agent uses a
model such as DeepSeek V4.1 Flash, with multimodal image input, context over
existing State notes and a constrained State-only tool set. Speech-to-text uses
a dedicated OpenRouter transcription model such as a Whisper or NVIDIA
alternative. State's MCP server and `statectl` CLI must receive corresponding
notes access.

This transcript records product intent. The normative design and constraints
are in [`ai-managed-notes.md`](ai-managed-notes.md).
