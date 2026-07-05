package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"
	"unsafe"

	arg "github.com/alexflint/go-arg"
	"github.com/disintegration/imaging"
	"github.com/dustin/go-humanize"
	"github.com/eiannone/keyboard"
	"github.com/mackerelio/go-osstat/cpu"
	"github.com/mackerelio/go-osstat/memory"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/unix"
)

//go:embed fontface.otf
var fontFaceData []byte

var version = "v1.0.0"

type fb_bitfield struct {
	offset    uint32
	length    uint32
	msb_right uint32
}

type fb_var_screeninfo struct {
	xres           uint32
	yres           uint32
	xres_virtual   uint32
	yres_virtual   uint32
	xoffset        uint32
	yoffset        uint32
	bits_per_pixel uint32
	grayscale      uint32

	red    fb_bitfield
	green  fb_bitfield
	blue   fb_bitfield
	transp fb_bitfield

	nonstd   uint32
	activate uint32
	height   uint32
	width    uint32

	accel_flags uint32

	pixclock     uint32
	left_margin  uint32
	right_margin uint32
	upper_margin uint32
	lower_margin uint32
	hsync_len    uint32
	vsync_len    uint32
	sync         uint32
	vmode        uint32
	rotate       uint32
	colorspace   uint32
	reserved     [4]uint32
}

const FBIOGET_FSCREENINFO = 0x4602
const FBIOGET_VSCREENINFO = 0x4600
const FBIOPAN_DISPLAY = 0x4606
const KDSETMODE = 0x4B3A
const KD_TEXT = 0x00
const KD_GRAPHICS = 0x01

type args struct {
	ImgPath    []string `arg:"positional,required"`
	DevicePath string   `default:"/dev/fb0"`
	Transform  []string `arg:"separate" help:"can be invoked multiple times\n                         accepted: fit hfit vfit center"`
	DontClear  bool     `help:"do not clear screen before rendering image"`
	NoCursor   bool     `help:"hide console cursor"`
	Redraw     int      `help:"keep re-rendering image every n seconds, hiding console output"`
	Stats      bool     `help:"display server statistics"`
	Format     []string `arg:"separate" help:"display format for statistics"`
	TextRot    int      `help:"text orientation (0, 90, 180, 270)"`
	TextScale  int      `help:"text scale, a multiplier"`
	Overlay    string   `help:"a picture to overlay, for instance as a text background"`
	Verbose    bool
}

type imgContext struct {
	image          *image.NRGBA
	image_width    int
	image_height   int
	image_xoffset  int
	image_yoffset  int
	screen_xoffset int
	screen_yoffset int
}

type displayInfo struct {
	X              int
	Y              int
	Size           int
	Red            uint8
	Green          uint8
	Blue           uint8
	ColorTransform Operation
	Template       string
	Output         string
}

type infoTemplate struct {
	TotalRam  string
	UsedRam   string
	FreeRam   string
	TotalSwap string
	UsedSwap  string
	FreeSwap  string
	CpuUser   string
	CpuSystem string
	CpuIdle   string
}

type ramStatsDetail struct {
	Total uint64
	Used  uint64
	Free  uint64
}

type cpuStatsDetail struct {
	Total  uint64
	User   uint64
	System uint64
	Idle   uint64
}

type stats struct {
	Ram        ramStatsDetail
	Swap       ramStatsDetail
	CpuInstant cpuStatsDetail
	CpuDelta   cpuStatsDetail
}

type Operation uint8

const (
	Col Operation = iota
	Or
	Xor
)

func (args) Description() string {
	return "Display an image in your graphical console using the frame buffer.\nYou may apply multiple transformations.\n"
}

func (args) Version() string {
	return version
}

func main() {
	var args args
	arg.MustParse(&args)

	// Check that formats are parseable
	formatInfo, err := parseFormat(args.Format, nil, nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	templates := []*template.Template{}
	for _, formatStr := range formatInfo {
		oneGoTemplate, err := template.New("display").Parse(formatStr.Template)
		if err != nil {
			fmt.Println(err)
			return
		}
		templates = append(templates, oneGoTemplate)
	}

	if args.TextScale == 0 {
		args.TextScale = 1
	} else if args.TextScale < 0 {
		fmt.Println("text scale must be positive")
		return
	}

	if !validTextRotation(args.TextRot) {
		fmt.Println("text orientation must be one of 0, 90, 180, 270")
		return
	}

	if len(formatInfo) > 255 {
		fmt.Println("at most 255 format entries are supported")
		return
	}

	if args.Redraw < 0 {
		fmt.Println("redraw interval must be zero or positive")
		return
	}

	fbF, err := os.OpenFile(args.DevicePath, os.O_RDWR, os.ModeDevice)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer fbF.Close()

	restoreConsole, err := setupConsole(args.Redraw > 0, args.NoCursor, args.Verbose)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer restoreConsole()

	screeninfo := fb_var_screeninfo{}
	uscreeninfo := unsafe.Pointer(&screeninfo)
	_, _, err = syscall.Syscall(syscall.SYS_IOCTL, fbF.Fd(), FBIOGET_VSCREENINFO, uintptr(uscreeninfo))
	if int(err.(syscall.Errno)) != 0 {
		fmt.Println(err)
		return
	}
	screen_width := int(screeninfo.xres)
	screen_height := int(screeninfo.yres)
	bpp := int(screeninfo.bits_per_pixel / 8)
	if bpp != 4 {
		fmt.Println("only 32-bit framebuffers are supported")
		return
	}
	if args.Verbose {
		fmt.Println("Screen information:", screen_width, screen_height, bpp)
	}

	var overlay image.Image
	if args.Overlay != "" {
		overlay, err = decodeImage(args.Overlay)
		if err != nil {
			fmt.Println(err)
			return
		}
	}

	var fontFaces []font.Face
	if len(formatInfo) > 0 {
		fontFaces, err = buildFontFaces(formatInfo, args.TextScale)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer closeFontFaces(fontFaces)
	}

	imageContexts := []imgContext{}

	for _, imgPath := range args.ImgPath {
		imageContext := imgContext{}

		img, err := decodeImage(imgPath)
		if err != nil {
			fmt.Println(err)
			return
		}

		var wImg image.Image
		wImg = img
		for _, transform := range args.Transform {
			imageContext.image_xoffset, imageContext.image_yoffset, imageContext.screen_xoffset, imageContext.screen_yoffset = 0, 0, 0, 0
			if transform == "fit" {
				if args.Verbose {
					fmt.Println("Image size before resizing:", wImg.Bounds())
				}
				wImg = imaging.Resize(wImg, screen_width, screen_height, imaging.Lanczos)
				if args.Verbose {
					fmt.Println("Image size after resizing:", wImg.Bounds())
				}
			} else if transform == "hfit" {
				if args.Verbose {
					fmt.Println("Image size before horizontal resizing:", wImg.Bounds())
				}
				wImg = imaging.Resize(wImg, screen_width, wImg.Bounds().Dy(), imaging.Lanczos)
				if args.Verbose {
					fmt.Println("Image size after resizing:", wImg.Bounds())
				}
			} else if transform == "vfit" {
				if args.Verbose {
					fmt.Println("Image size before vertical resizing:", wImg.Bounds())
				}
				wImg = imaging.Resize(wImg, wImg.Bounds().Dx(), screen_height, imaging.Lanczos)
				if args.Verbose {
					fmt.Println("Image size after resizing:", wImg.Bounds())
				}
			} else if transform == "center" {
				imgWidth := wImg.Bounds().Max.X
				imgHeight := wImg.Bounds().Max.Y
				if imgWidth > screen_width {
					imageContext.image_xoffset = (imgWidth - screen_width) / 2
				} else if imgWidth < screen_width {
					imageContext.screen_xoffset = (screen_width - imgWidth) / 2
				}
				if imgHeight > screen_height {
					imageContext.image_yoffset = (imgHeight - screen_height) / 2
				} else if imgHeight < screen_height {
					imageContext.screen_yoffset = (screen_height - imgHeight) / 2
				}
				if args.Verbose {
					fmt.Println("Image size:", wImg.Bounds())
				}
			}
		}

		imageContext.image = toNRGBA(wImg)
		imageContext.image_width = imageContext.image.Bounds().Dx()
		if imageContext.image_width > screen_width {
			imageContext.image_width = screen_width
		}
		imageContext.image_height = imageContext.image.Bounds().Dy()
		if imageContext.image_height > screen_height {
			imageContext.image_height = screen_height
		}
		if args.Verbose {
			fmt.Println("y from", imageContext.image_yoffset, "to", imageContext.image_yoffset+imageContext.image_height, "x from", imageContext.image_xoffset, "to", imageContext.image_xoffset+imageContext.image_width)
			fmt.Println("screen y from", imageContext.screen_yoffset, "screen x from", imageContext.screen_xoffset)
		}

		imageContexts = append(imageContexts, imageContext)
	}

	screenPixels, err := syscall.Mmap(
		int(fbF.Fd()),
		0,
		screen_width*screen_height*bpp,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer syscall.Munmap(screenPixels)
	framePixels := make([]byte, len(screenPixels))

	keysEvents, err := keyboard.GetKeys(1)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() {
		_ = keyboard.Close()
	}()
	signalEvents := make(chan os.Signal, 1)
	signal.Notify(signalEvents, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalEvents)

	curImageContextIdx := 0
	curStats := stats{}
	frameNumber := 0
	for {
		if !args.DontClear {
			for i := 0; i < len(framePixels); i++ {
				framePixels[i] = 0
			}
		} else {
			copy(framePixels, screenPixels)
		}

		if args.Verbose {
			fmt.Println("Reading image:", curImageContextIdx)
		}
		imageContext := imageContexts[curImageContextIdx]

		// If we need to display e.g. stats, we are going to create a mask, that we will
		// then use on the existing picture.
		// As any basic mask, it is going to use black and white only.
		// The alpha channel will be used to store the local mask definition index.
		var mask *image.NRGBA
		var formatInfoList []displayInfo
		if args.Stats {
			err = updateStats(&curStats)
			if err != nil {
				fmt.Println(err)
				return
			}

			// Format:
			// "X:100;Y:100;S:12;C:255,255,255;RAM:{{.TotalRam}}"
			// C: can be
			// - R,G,B
			// - xor
			// Missing X or Y: try to center
			formatInfoList, _ = parseFormat(args.Format, &curStats, templates)
			if args.TextRot == 0 || args.TextRot == 180 {
				mask = image.NewNRGBA(image.Rect(0, 0, screen_width, screen_height))
			} else {
				mask = image.NewNRGBA(image.Rect(0, 0, screen_height, screen_width))
			}
			for idx, formatInfo := range formatInfoList {
				d := &font.Drawer{
					Dst:  mask,
					Src:  image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: uint8(idx + 1)}),
					Face: fontFaces[idx],
					Dot:  fixed.Point26_6{X: fixed.I(formatInfo.X), Y: fixed.I(formatInfo.Y)},
				}
				d.DrawString(formatInfo.Output)
			}

			if args.TextRot == 180 {
				mask = imaging.Rotate180(mask)
			} else if args.TextRot == 90 {
				mask = imaging.Rotate90(mask)
			} else if args.TextRot == 270 {
				mask = imaging.Rotate270(mask)
			}
		}
		textLayerCount := uint8(len(formatInfoList))

		curPixelBit := (imageContext.screen_yoffset*screen_width + imageContext.screen_xoffset) * 4
		for y := imageContext.image_yoffset; y < imageContext.image_yoffset+imageContext.image_height; y++ {
			screenY := imageContext.screen_yoffset + y - imageContext.image_yoffset
			for x := imageContext.image_xoffset; x < imageContext.image_xoffset+imageContext.image_width; x++ {
				screenX := imageContext.screen_xoffset + x - imageContext.image_xoffset
				pixColor := imageContext.image.At(x, y)
				pixColorBits := pixColor.(color.NRGBA)
				if overlay != nil {
					pixColorBits = blendOverlay(pixColorBits, overlay, screenX, screenY)
				}
				if args.Stats {
					maskColor := mask.At(screenX, screenY)
					maskColorBits := maskColor.(color.NRGBA)
					if maskColorBits.A != 0 {
						var idx uint8
						if maskColorBits.A >= textLayerCount {
							idx = textLayerCount - 1
						} else {
							idx = maskColorBits.A - 1
						}
						framePixels[curPixelBit] = twist(pixColorBits.B, maskColorBits.B,
							formatInfoList[idx].ColorTransform, formatInfoList[idx].Blue)
						curPixelBit++
						framePixels[curPixelBit] = twist(pixColorBits.G, maskColorBits.G,
							formatInfoList[idx].ColorTransform, formatInfoList[idx].Green)
						curPixelBit++
						framePixels[curPixelBit] = twist(pixColorBits.R, maskColorBits.R,
							formatInfoList[idx].ColorTransform, formatInfoList[idx].Red)
						curPixelBit++
						framePixels[curPixelBit] = 255
						curPixelBit++
						continue
					}
				}
				framePixels[curPixelBit] = pixColorBits.B
				curPixelBit++
				framePixels[curPixelBit] = pixColorBits.G
				curPixelBit++
				framePixels[curPixelBit] = pixColorBits.R
				curPixelBit++
				framePixels[curPixelBit] = pixColorBits.A
				curPixelBit++
			}
			if screen_width > imageContext.image_width {
				curPixelBit += (screen_width - imageContext.image_width) * 4
			}
		}

		copy(screenPixels, framePixels)
		flushFramebuffer(fbF, screenPixels, &screeninfo, args.Verbose)
		if args.Verbose {
			fmt.Println("Rendered frame:", frameNumber, "image:", curImageContextIdx, "next redraw:", args.Redraw)
		}
		frameNumber++

		if len(imageContexts) == curImageContextIdx+1 {
			if args.Redraw == 0 {
				break
			}
		}
		curImageContextIdx++
		if curImageContextIdx >= len(imageContexts) {
			curImageContextIdx = 0
		}

		for sleeper := 0; sleeper < args.Redraw*10; sleeper++ {
			select {
			case event := <-keysEvents:
				if event.Key == keyboard.KeyEsc {
					return
				}
			case <-signalEvents:
				return
			default:
			}

			time.Sleep(100 * time.Millisecond)
		}
	}
}

func twist(component1 uint8, component2 uint8, op Operation, reqcomponent uint8) uint8 {
	switch op {
	case Xor:
		return component1 ^ component2
	case Or:
		return component1 | component2
	default:
		return reqcomponent
	}
}

func blendOverlay(base color.NRGBA, overlay image.Image, screenX int, screenY int) color.NRGBA {
	point := image.Point{X: screenX, Y: screenY}
	if !point.In(overlay.Bounds()) {
		return base
	}

	top := color.NRGBAModel.Convert(overlay.At(screenX, screenY)).(color.NRGBA)
	if top.A == 0 {
		return base
	}
	if top.A == 255 {
		return top
	}

	alpha := uint32(top.A)
	invAlpha := 255 - alpha
	return color.NRGBA{
		R: uint8((uint32(top.R)*alpha + uint32(base.R)*invAlpha) / 255),
		G: uint8((uint32(top.G)*alpha + uint32(base.G)*invAlpha) / 255),
		B: uint8((uint32(top.B)*alpha + uint32(base.B)*invAlpha) / 255),
		A: 255,
	}
}

func flushFramebuffer(fbF *os.File, screenPixels []byte, screeninfo *fb_var_screeninfo, verbose bool) {
	err := unix.Msync(screenPixels, unix.MS_SYNC)
	if err != nil && verbose {
		fmt.Println("Framebuffer sync:", err)
	}

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fbF.Fd(), FBIOPAN_DISPLAY, uintptr(unsafe.Pointer(screeninfo)))
	if errno != 0 && verbose {
		fmt.Println("Framebuffer pan:", errno)
	}
}

func setupConsole(graphicsMode bool, hideCursor bool, verbose bool) (func(), error) {
	if !graphicsMode && !hideCursor {
		return func() {}, nil
	}

	console, consolePath, err := openConsole(graphicsMode)
	if err != nil {
		return nil, err
	}
	if verbose {
		fmt.Println("Using console:", consolePath)
	}

	if hideCursor {
		_, _ = console.WriteString("\033[?25l")
	}

	return func() {
		if graphicsMode {
			_ = setConsoleMode(console, KD_TEXT)
		}
		if hideCursor {
			_, _ = console.WriteString("\033[?25h")
			time.Sleep(1 * time.Second)
		}
		_ = console.Close()
	}, nil
}

func setConsoleMode(console *os.File, mode int) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, console.Fd(), KDSETMODE, uintptr(mode))
	if errno != 0 {
		return errno
	}
	return nil
}

func openConsole(requireGraphicsMode bool) (*os.File, string, error) {
	var lastErr error
	for _, path := range []string{"/dev/tty", "/dev/tty0", "/dev/console"} {
		console, err := os.OpenFile(path, unix.O_RDWR, 0)
		if err != nil {
			lastErr = err
			continue
		}

		if !requireGraphicsMode {
			return console, path, nil
		}

		err = setConsoleMode(console, KD_GRAPHICS)
		if err == nil {
			return console, path, nil
		}

		lastErr = fmt.Errorf("%s: %w", path, err)
		_ = console.Close()
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no usable console device found")
	}
	return nil, "", lastErr
}

func decodeImage(path string) (image.Image, error) {
	imgF, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer imgF.Close()

	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return png.Decode(imgF)
	case ".jpg", ".jpeg":
		return jpeg.Decode(imgF)
	default:
		return nil, fmt.Errorf("unsupported image format: %s", path)
	}
}

func toNRGBA(src image.Image) *image.NRGBA {
	if img, ok := src.(*image.NRGBA); ok {
		return img
	}

	bounds := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Src)
	return dst
}

func buildFontFaces(formatInfo []displayInfo, textScale int) ([]font.Face, error) {
	parsedFont, err := opentype.Parse(fontFaceData)
	if err != nil {
		return nil, fmt.Errorf("parse font data: %w", err)
	}

	faces := make([]font.Face, 0, len(formatInfo))
	for _, info := range formatInfo {
		face, err := opentype.NewFace(parsedFont, &opentype.FaceOptions{
			Size:    float64(info.Size * textScale),
			DPI:     72.0,
			Hinting: font.HintingNone,
		})
		if err != nil {
			closeFontFaces(faces)
			return nil, fmt.Errorf("build font face: %w", err)
		}
		faces = append(faces, face)
	}

	return faces, nil
}

func closeFontFaces(fontFaces []font.Face) {
	for _, fontFace := range fontFaces {
		closer, ok := fontFace.(interface{ Close() error })
		if !ok {
			continue
		}
		_ = closer.Close()
	}
}

func validTextRotation(textRotation int) bool {
	switch textRotation {
	case 0, 90, 180, 270:
		return true
	default:
		return false
	}
}

func updateStats(curStats *stats) error {
	memoryInfo, err := memory.Get()
	if err != nil {
		return err
	}
	curStats.Ram = ramStatsDetail{Total: memoryInfo.Total, Used: memoryInfo.Used, Free: memoryInfo.Free}
	curStats.Swap = ramStatsDetail{Total: memoryInfo.SwapTotal, Used: memoryInfo.SwapUsed, Free: memoryInfo.SwapFree}

	cpuInfo, err := cpu.Get()
	if err != nil {
		return err
	}

	previous := curStats.CpuInstant
	current := cpuStatsDetail{Total: cpuInfo.Total, User: cpuInfo.User, System: cpuInfo.System, Idle: cpuInfo.Idle}
	curStats.CpuDelta = cpuStatsDetail{}
	if previous.Total != 0 && current.Total > previous.Total {
		totalDelta := current.Total - previous.Total
		curStats.CpuDelta = cpuStatsDetail{
			Total:  totalDelta,
			User:   percentage(current.User-previous.User, totalDelta),
			System: percentage(current.System-previous.System, totalDelta),
			Idle:   percentage(current.Idle-previous.Idle, totalDelta),
		}
	}
	curStats.CpuInstant = current

	return nil
}

func percentage(component uint64, total uint64) uint64 {
	if total == 0 {
		return 0
	}
	return component * 100 / total
}

func parseColorComponent(component string) (uint8, error) {
	value, err := strconv.Atoi(component)
	if err != nil {
		return 0, err
	}
	if value < 0 || value > 255 {
		return 0, fmt.Errorf("color component out of range: %d", value)
	}
	return uint8(value), nil
}

func parseFormat(formatStrList []string, statsStruct *stats, templates []*template.Template) ([]displayInfo, error) {
	var err error
	allinfo := []displayInfo{}
	for idx, formatStr := range formatStrList {
		info := displayInfo{}
		format := strings.Split(formatStr, ";")
		updatedX, updatedY, updatedC := false, false, false
		for _, hint := range format {
			if strings.HasPrefix(hint, "X:") {
				info.X, err = strconv.Atoi(hint[2:])
				if err != nil {
					return allinfo, err
				}
				updatedX = true
			} else if strings.HasPrefix(hint, "Y:") {
				info.Y, err = strconv.Atoi(hint[2:])
				if err != nil {
					return allinfo, err
				}
				updatedY = true
			} else if strings.HasPrefix(hint, "S:") {
				info.Size, err = strconv.Atoi(hint[2:])
				if err != nil {
					return allinfo, err
				}
				if info.Size < 1 {
					return allinfo, fmt.Errorf("text size must be positive: %d", info.Size)
				}
			} else if strings.HasPrefix(hint, "C:") {
				transform := hint[2:]
				if transform == "xor" {
					info.ColorTransform = Xor
				} else if transform == "or" {
					info.ColorTransform = Or
				} else {
					colors := strings.Split(hint[2:], ",")
					if len(colors) != 3 {
						return allinfo, fmt.Errorf("invalid color format: %s", hint[2:])
					}
					info.Red, err = parseColorComponent(colors[0])
					if err != nil {
						return allinfo, err
					}
					info.Green, err = parseColorComponent(colors[1])
					if err != nil {
						return allinfo, err
					}
					info.Blue, err = parseColorComponent(colors[2])
					if err != nil {
						return allinfo, err
					}
				}
				updatedC = true
			} else {
				info.Template = hint
			}
		}
		if !updatedX {
			info.X = 100
		}
		if !updatedY {
			info.Y = 100
		}
		if info.Size == 0 {
			info.Size = 12
		}
		if !updatedC {
			info.Red = 255
			info.Green = 255
			info.Blue = 255
		}
		if statsStruct != nil {
			curInfoTemplate := infoTemplate{
				TotalRam:  humanize.Bytes(statsStruct.Ram.Total),
				UsedRam:   humanize.Bytes(statsStruct.Ram.Used),
				FreeRam:   humanize.Bytes(statsStruct.Ram.Free),
				TotalSwap: humanize.Bytes(statsStruct.Swap.Total),
				UsedSwap:  humanize.Bytes(statsStruct.Swap.Used),
				FreeSwap:  humanize.Bytes(statsStruct.Swap.Free),
				CpuUser:   strconv.FormatUint(statsStruct.CpuDelta.User, 10),
				CpuSystem: strconv.FormatUint(statsStruct.CpuDelta.System, 10),
				CpuIdle:   strconv.FormatUint(statsStruct.CpuDelta.Idle, 10),
			}
			var b bytes.Buffer
			err = templates[idx].Execute(&b, curInfoTemplate)
			if err != nil {
				return allinfo, err
			}
			info.Output = b.String()
		}
		allinfo = append(allinfo, info)
	}
	return allinfo, nil
}
