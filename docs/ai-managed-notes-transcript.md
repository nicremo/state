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

## Original wording

Fabian's spoken request, verbatim (German). This is the binding source for
Stage B; the summary above only indexes it.

> Es geht wieder um State, um meine App State, so wie gestern. Schau mal ins Repository, schau auf GitHub. Da habe ich die State App. S-T-A-T-E. Hast du gestern zum Beispiel schon mal einen Pull Request gemacht mit einer Dokumentation? Das Gleiche jetzt auch. Und zwar in State muss es eine Dead Simple, also Tod Simple Notizfunktion geben. Eins zu eins wie Apple Notizen. Das hat man dann einfach unten in der Navigationsbar. Da drückst du auf Notizen. So. Und diese Notizen sind Dead Simple. Du kannst nicht mal kategorisieren und so weiter, zumindest nicht in der Suche, sondern die sind einfach gelistet immer. Du siehst immer den Titel und eine kurze Zusammenfassung. Und jetzt kommt es zum Schlüssel. Diese ganzen Notizen, die sind absolut KI-basiert. Also nicht KI-basiert, sondern KI-gemanagt. Folgendes: Wir bauen in die State-App ein eigenes LLM ein. Und zwar binden wir das über OpenRouter ein. Dann nehmen wir dann sowas wie DeepSeek V4.1 Flash und binden das an für folgende Zwecke. Und zwar bauen wir in die App eine Notiz-Sektion. Wirklich noch simpler als die iPhone-Notizen-App mit der gleichen Formatierungsoption. Das kannst du ja schon mal alles recherchieren und dann mit in die Dokumentation schreiben, damit wir das alles schon mal grundlegend gesetzt haben. Und zwar Hat man einen Plus-Button, den großen, wenn man diesen unten rechts, wenn man auf das Plus-Button drückt, kommt Text. Also man schreibt selbst eine Notiz oder Bild oder Sprache. Und zwar, dann drücke ich auch und und und dann brauchen wir noch ein Transkriptionsmodell. Das können wir auch über OpenRouter anbinden, weil da gibt es die ganzen Transkriptionsmodelle wie Nemotron von Nvidia oder die ganzen Whisper-Modelle. Mega wichtig, die müssen wir dann anbinden, damit damit wir Speech-to-Text haben. Also damit man auch reinsprechen kann und das halt praktisch für das LLM in Text transkribiert wird. Und zwar es geht einzig und allein darum der Hauptfokus ist wir machen also du drückst auf das Plus dann hast du Text Bild Audio wenn du auf Text drückst ist das wie in den iPhone Notizen du schreibst den Titel und deine Notiz drückst du auf Bild hast du die Möglichkeit Fotos zu machen und die sollen begrenzt sein oder ne unbegrenzt sein im besten Fall so viel wie eben das Vision Modell was man benutzt da muss natürlich dann abgeklärt sein ob Deepseek v4.1 Flash multimodal ist und auch Bilder verarbeiten kann auf jeden Fall die maximale Anzahl der maximal zu verarbeitenden Bilder des LLMs dort als maximal Auswahl rein und dann kann ich zum Beispiel aus meinem Notizbuch, was ich per Handschreiben Bilder machen und diese Notizen aus meinem Buch werden automatisch in die Notizen-App gepackt und im besten Falle hat das auch noch den Gesamtkontext aller bereits vorhandenen Notizen. Also das OpenRouter LLM, was wir da anbinden, soll ein komplett gebauter Agent sein, gegebenenfalls über MASTRA SDK oder das Versal AI SDK. Damit das ein Agent ist, der auch Toolcalls machen kann und so weiter. Und diese Toolcalls sollen aber größtenteils beschränkt sein auf die State App, dass wir zum Beispiel Erinnerungen haben oder bereits vorhandene Notizen, die basieren auf diesen Notizen und dass alles dann so fungiert miteinander. Wichtig ist auch für diese Notizen muss natürlich das CLI Tool und der MCP Server auch entsprechend komplett erweitert werden, weil auch dieser den Zugriff natürlich auf diese Notizen dann braucht. Das ist die Funktion, die möchte ich da drin haben. Eine richtig tot simple Notizen Möglichkeit.

This transcript records product intent. The normative design and constraints
are in [`ai-managed-notes.md`](ai-managed-notes.md).
