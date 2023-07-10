package main

import (
	"C"
	"fmt"
	"os"
)
import (
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/eiannone/keyboard"
	"golang.org/x/sys/unix"

	arg "github.com/alexflint/go-arg"
	"github.com/disintegration/imaging"
)

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

type args struct {
	ImgPath    []string `arg:"positional,required"`
	DevicePath string   `default:"/dev/fb0"`
	Transform  []string `arg:"separate" help:"can be invoked multiple times\n                         accepted: fit hfit vfit center"`
	DontClear  bool     `help:"do not clear screen before rendering image"`
	NoCursor   bool     `help:"hide console cursor"`
	Redraw     int      `help:"keep re-rendering image every n seconds, hiding console output"`
	Verbose    bool
}

type imgContext struct {
	image          image.Image
	image_width    int
	image_height   int
	image_xoffset  int
	image_yoffset  int
	screen_xoffset int
	screen_yoffset int
}

func (args) Description() string {
	return "Display an image in your graphical console using the frame buffer.\nYou may apply multiple transformations.\n"
}

func main() {
	var args args
	arg.MustParse(&args)

	fbF, err := os.OpenFile(args.DevicePath, os.O_RDWR, os.ModeDevice)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer fbF.Close()

	if args.NoCursor {
		fbT, err := os.OpenFile("/dev/console", unix.O_WRONLY, 0)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer func() {
			fbT.WriteString("\033[?25h")
			time.Sleep(1 * time.Second)
			fbT.Close()
		}()
		fbT.WriteString("\033[?25l")
	}

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
	if args.Verbose {
		fmt.Println("Screen information:", screen_width, screen_height, bpp)
	}

	imageContexts := []imgContext{}

	for _, imgPath := range args.ImgPath {
		imageContext := imgContext{}

		imgF, err := os.Open(imgPath)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer imgF.Close()

		var img image.Image
		if strings.HasSuffix(imgPath, ".png") {
			img, err = png.Decode(imgF)
		} else if strings.HasSuffix(imgPath, ".jpg") || strings.HasSuffix(imgPath, ".jpeg") {
			img, err = jpeg.Decode(imgF)
		}
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

		_, ok := wImg.At(0, 0).(color.NRGBA)
		if !ok {
			convertedImg := image.NewNRGBA(image.Rect(0, 0, wImg.Bounds().Dx(), wImg.Bounds().Dy()))
			draw.Draw(convertedImg, convertedImg.Bounds(), wImg, wImg.Bounds().Min, draw.Src)
			wImg = convertedImg
		}

		imageContext.image = wImg
		imageContext.image_width = wImg.Bounds().Max.X
		if imageContext.image_width > screen_width {
			imageContext.image_width = screen_width
		}
		imageContext.image_height = wImg.Bounds().Max.Y
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

	keysEvents, err := keyboard.GetKeys(1)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() {
		_ = keyboard.Close()
	}()

	curImageContextIdx := 0
	for {
		if !args.DontClear {
			for i := 0; i < screen_height*screen_width*4; i++ {
				screenPixels[i] = 0
			}
		}

		if args.Verbose {
			fmt.Println("Reading image:", curImageContextIdx)
		}
		imageContext := imageContexts[curImageContextIdx]

		curPixelBit := (imageContext.screen_yoffset*screen_width + imageContext.screen_xoffset) * 4
		for y := imageContext.image_yoffset; y < imageContext.image_yoffset+imageContext.image_height; y++ {
			for x := imageContext.image_xoffset; x < imageContext.image_xoffset+imageContext.image_width; x++ {
				pixColor := imageContext.image.At(x, y)
				pixColorBits := pixColor.(color.NRGBA)
				screenPixels[curPixelBit] = pixColorBits.B
				curPixelBit++
				screenPixels[curPixelBit] = pixColorBits.G
				curPixelBit++
				screenPixels[curPixelBit] = pixColorBits.R
				curPixelBit++
				screenPixels[curPixelBit] = pixColorBits.A
				curPixelBit++
			}
			if screen_width > imageContext.image_width {
				curPixelBit += (screen_width - imageContext.image_width) * 4
			}
		}

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
			default:
			}

			time.Sleep(100 * time.Millisecond)
		}
	}
}
