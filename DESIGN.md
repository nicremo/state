# Design

State is mini-minimal and native: printed ink on cool paper, one idea per screen, system controls and system type. The icon (black glossy "S", yellow dot, pale blue-grey ground) is the only ornament.

## Color

Restrained: neutrals plus ink. Tokens live in `ios/State/Sources/UI/StateTheme.swift`.

| Token | Light | Dark | Role |
| --- | --- | --- | --- |
| `accent` (ink) | #121316 | #F3F5F7 | The one interactive color: filled buttons, tint |
| `onAccent` | white | #0B0C0E | Text on ink |
| `accentSoft` | ink 6 % | ink 12 % | Secondary button fill |
| `ground` | #F6F7F9 | #0B0C0E | Every screen |
| `mist` | #DEE7F0 | #0B0C0E | Launch screen and splash, sampled from the icon ground |
| `signal` | #E0DD28 | same | The icon's dot. At most once per screen, never for text |
| `graphite` | #121316 | #EBEDF0 | Primary text |

Dark appearance is first class. No gradients on UI surfaces, no glass as decoration.

## Type

San Francisco through system text styles only (Dynamic Type). Screen statements use `.title` or `.largeTitle` bold; supporting copy `.body` secondary; rows `.headline` plus `.subheadline` secondary.

## Controls

- `StatePrimaryButtonStyle`: full ink fill, semibold label. 54 pt high, 16 pt radius on iPhone and iPad; 40 pt, 10 pt radius on the Mac.
- `StateSecondaryButtonStyle`: `accentSoft` fill, ink label, same geometry.
- One primary action per screen. Links are plain footnote text in secondary color.
- Forms are native grouped `Form`s; on the Mac capped at 600 pt and centered.

## Layout

- iPhone: statement in the optical center, actions pinned above the home indicator.
- Mac: one centered column; actions 320 pt wide directly under the statement, never pinned to the window edge. Windows open at 1080 x 720.
- Spacing scale in `StateTheme.Space` (2, 4, 6, 8, 12, 16, 24, 32).

## Motion

One authored moment per screen: the splash mark settles (0.5 s smooth), welcome points rise 10 pt in sequence. Everything else uses `StateTheme.stateChange`. Reduce Motion removes movement and keeps crossfades.

## First run

Launch color = splash color (`mist`), so the tapped icon seems to open. Welcome is one screen. Connection offers the code path first (scan on iPhone, paste on Mac), the typed address second, and asks for notification permission only after a server is connected.
