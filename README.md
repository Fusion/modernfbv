# What is this?

![Example](/assets/modernfbv-demo.gif)

You may or may not remember the venerable (circa 2000) FrameBuffer image Viewer.

"Modern" Linux comes with a framebuffer subsystem, allowing it to display graphics in its otherwise boring console (think the Tux logo when booting up)

Believe it or not, people used to write games that you could play without installing any graphic environment on your desktop.

Anyway, I thought it would be a fun exercise to revive this idea. I spend a fair amount of time working with virtual machines and opening their VNC console. Doesn't that sound like a perfect environment to display a kitten picture? Other (admittedly non-critical) uses include displaying the weather in my TrueNAS console, etc.

# The Future

I may the ability to display real-time data, such as the server's health or some arbitrary text. Let me know if this would tickle your fancy.

# Pure GO 

While I found other projects trying to talk to the framebufer, they were all going through an intermediate piece of C code. I felt that Go should be able to perform system calls and memory mapping. I was right.

# Usage

1. Download the binary from the Releases page
2. Make a few pictures available on the server
3. Run `modernfbv`

Example:

```
/modernfbv  --transform hfit  --transform center \
    --redraw 1 --nocursor \
    1.png 2.png 3.png
```

This would display a slideshow of three images, refreshed every second; each image horizontally fitted then centered; while hiding the prompt cursor to keep things looking good.