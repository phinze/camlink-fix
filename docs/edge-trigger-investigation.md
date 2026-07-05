# Edge-trigger investigation: "need the camera" instead of "plugged in"

Notes-to-self for a follow-up session. Context is fresh now (2026-07); it
won't be later.

## The idea

Today the daemon fires its check-and-reset on environmental events: `wake`,
`usb-arrival`, `startup`, plus the manual `SIGUSR1` kick. Three of those four
are "something changed in my hardware environment," which means the camera gets
reset on every lid-open whether or not you're about to use it. On a meeting-free
week that's pure downside: unnecessary power-cycles (and notifications) for a
camera you were happy to leave off.

We want to move the trigger from "plugged the laptop in" toward "an app actually
reached for the camera." The open question was whether macOS even exposes a
usable "camera demanded" signal. It does.

## What we found (the signal)

macOS logs camera activity through CoreMediaIO. Streaming the unified log
(`log stream --predicate 'subsystem == "com.apple.cmio" ...'`) surfaces markers
when an app opens a camera. There is no single clean "camera opened" line;
instead there are three, and which one fires depends on state:

- **cold-start** — `CMIO_DAL_System.cpp:...:CheckOutInstance The System is
  starting`. Fires only when the app is the first CMIO client after the system
  was fully torn down.
- **warm-open** — `CMIO_DAL_System.cpp:...:Get System unsuspended`. Fires on a
  normal open when the DAL system was already alive. Bursts ~17x per open.
- **device-control** — `...:SetPropertyData ... setting deviceControlPID`.
  Fires when a client takes (or releases) control of an actual device. Closest
  thing to "about to stream." This looks like the most promising signal.

Crucially, all three are logged by the **client app itself**, so the ndjson
`processImagePath` is the app (e.g. "Photo Booth"), not a system daemon. We get
attribution for free. (`process` is often null in ndjson; fall back to the
basename of `processImagePath`.)

## What we built (this is shipped as observe-only)

`internal/camwatch/watcher_darwin.go` tails `log stream --style ndjson` for all
three markers, tags each with its signal name, debounces the per-open burst
(3s per process+signal), and emits an `Event{Process, Signal}`. It is wired into
`cmd/camlink-fix/main.go` but **drives nothing** — the select case just logs:

```
camera-open observed (app="Photo Booth" signal=device-control) — not acting (observe-only)
```

The whole point of shipping it observe-only is to collect real-world data across
scenarios we can't reproduce on the couch, before committing to the rework.

## Where to look

Log lands in `/tmp/camlink-fix.log` (the launchd agent redirects stdout/stderr
there). To scan for real app opens, filter out our own health-check probes:

```
grep 'camera-open observed' /tmp/camlink-fix.log | grep -vE 'system_profiler|ffmpeg'
```

`system_profiler` and `ffmpeg` are the daemon's *own* health check opening the
camera, and they look identical to any other app. The apps that matter are the
real ones: `zoom.us`, `Google Chrome` (browser-based Meet shows up as the
browser process, not "Meet"), Slack, FaceTime, etc.

## Questions the observe phase should answer

1. **Which signal reliably means "I need the camera"?** Hypothesis:
   `device-control`. Confirm against real meeting joins.
2. **THE make-or-break one: does the signal still fire when the Cam Link is
   wedged?** The whole edge-trigger idea depends on it. Can't be manufactured on
   demand; only happens after certain sleep/wake cycles. Next time your camera
   is black in a call, check whether camwatch logged a `device-control` around
   that timestamp.
3. **Do real meeting apps behave like Photo Booth?** Zoom vs a Meet tab in the
   browser vs Slack huddle vs FaceTime may emit different mixes of the three
   signals.
4. **What's the false-positive rate?** How much does the log light up during a
   normal day from background apps polling the camera?

Note: in pure observe-only mode we do NOT run a health check at open time, so
the log won't independently confirm the camera was wedged at that instant. We
correlate by timestamp against the wake-triggered health checks already in the
log. If that turns out too soft, we could add a passive health check on each
open, but that risks fighting the app for the device (and re-introduces the
ffmpeg self-trigger), so we left it out for now.

## Gotchas for the NEXT phase (wiring it to actually reset)

- **Feedback loop.** Our health check opens the camera (system_profiler +
  ffmpeg) → camwatch sees a camera-open → which would trigger another health
  check → loop. Before camwatch can drive a reset, suppress our own probe PIDs
  (or otherwise exclude our processes).
- **Latency tradeoff.** A reactive trigger means eating the ~10-20s reset
  latency at the moment you join the call, instead of the current model where
  wake pre-warms the camera before you sit down. This is the fundamental cost of
  going reactive.

## Options on the table (from the design discussion)

1. **Camera-demand hook** (what we built) — truest "need it" edge trigger, but
   pays the reset latency at join time, and depends on question #2 above.
2. **Calendar pre-warm** — check the calendar, run the fix a couple minutes
   before a meeting. Sidesteps the latency problem and stays silent on
   meeting-free weeks. Needs calendar access. Arguably the best fit since your
   meetings are calendared.
3. **Lean on the manual kick** — menu-bar item / hotkey, make the human the
   edge. Zero magic, but you have to remember it.

Likely landing spot: calendar-warm as primary with the manual kick as fallback,
and fold in the camera-open signal if question #2 validates. Demoting the `wake`
trigger is the common thread in all of them.

## Status / deploy

- Code committed and pushed to `main` (`2cbd91c`).
- nix-config `flake.lock` bumped to that rev; `darwin-rebuild build` validated
  clean.
- **PENDING:** the `darwin-rebuild switch` has NOT been run yet, so the live
  daemon is still the old build with no camwatch. Finish with:
  ```
  sudo darwin-rebuild switch --flake ~/src/github.com/phinze/nix-config#phinze-mrn-mbp
  ```
  Then confirm `camwatch: listening for camera-open events` shows up in
  `/tmp/camlink-fix.log`.
