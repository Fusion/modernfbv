# What is this?

![Example](/assets/modernfbv-demo.gif)

You may or may not remember the venerable (circa 2000) FrameBuffer image Viewer.

"Modern" Linux comes with a framebuffer subsystem, allowing it to display graphics in its otherwise boring console (think the Tux logo when booting up)

Believe it or not, people used to write games that you could play without installing any graphic environment on your desktop.

Anyway, I thought it would be a fun exercise to revive this idea. I spend a fair amount of time working with virtual machines and opening their VNC console. Doesn't that sound like a perfect environment to display a kitten picture? Other (admittedly non-critical) uses include displaying the weather in my TrueNAS console, etc.

# Stats Overlay

The viewer can render live system data on top of the image with `--stats`.
Each `--format` entry accepts optional `X:`, `Y:`, `S:` and `C:` hints followed by a Go template.

Available template fields:

- `{{.TotalRam}}`, `{{.UsedRam}}`, `{{.FreeRam}}`
- `{{.TotalSwap}}`, `{{.UsedSwap}}`, `{{.FreeSwap}}`
- `{{.CpuUser}}`, `{{.CpuSystem}}`, `{{.CpuIdle}}`

`--textscale` multiplies the per-line `S:` value, `--textrot` rotates the text layer, and `--overlay` composites another image before the text is drawn.
Use `--redraw` when the stats overlay should stay live and refresh periodically.
While `--redraw` is active, modernfbv switches the virtual terminal to graphics mode and restores text mode when it exits.

Example:

```
/modernfbv --transform fit --stats \
    --format 'X:80;Y:120;S:18;C:255,255,255;RAM {{.UsedRam}} / {{.TotalRam}}' \
    --format 'X:80;Y:170;S:18;C:xor;CPU {{.CpuUser}}% usr {{.CpuSystem}}% sys {{.CpuIdle}}% idle' \
    --textscale 2 --overlay stats-bg.png \
    --redraw 2 \
    1.png
```

# Pure GO 

While I found other projects trying to talk to the framebuffer, they were all going through an intermediate piece of C code. I felt that Go should be able to perform system calls and memory mapping. I was right.

# Usage

1. Download the binary from the Releases page
2. Make a few pictures available on the server
3. Run `modernfbv`

Check the binary version with:

```
/modernfbv --version
```

Builds default to `v1.0.0`. Override the embedded version with:

```
make build VERSION=v1.0.1
```

Example:

```
/modernfbv  --transform hfit  --transform center \
    --redraw 1 --nocursor \
    1.png 2.png 3.png
```

This would display a slideshow of three images, refreshed every second; each image horizontally fitted then centered; while hiding the prompt cursor to keep things looking good.
