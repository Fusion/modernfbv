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
	"unsafe"

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

var args struct {
	ImgPath    string   `arg:"positional,required"`
	DevicePath string   `default:"/dev/fb0"`
	Transform  []string `arg:"separate" help:"fit|center"`
	DontClear  bool
	Verbose    bool
}

func main() {
	arg.MustParse(&args)
	imgF, err := os.Open(args.ImgPath)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer imgF.Close()

	var img image.Image
	if strings.HasSuffix(args.ImgPath, ".png") {
		img, err = png.Decode(imgF)
	} else if strings.HasSuffix(args.ImgPath, ".jpg") || strings.HasSuffix(args.ImgPath, ".jpeg") {
		img, err = jpeg.Decode(imgF)
	}
	if err != nil {
		fmt.Println(err)
		return
	}

	fbF, err := os.OpenFile(args.DevicePath, os.O_RDWR, os.ModeDevice)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer fbF.Close()

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

	image_xoffset := 0
	image_yoffset := 0
	screen_xoffset := 0
	screen_yoffset := 0

	var wImg image.Image
	wImg = img
	for _, transform := range args.Transform {
		image_xoffset, image_yoffset, screen_xoffset, screen_yoffset = 0, 0, 0, 0
		if transform == "fit" {
			if args.Verbose {
				fmt.Println("Image size before resizing:", wImg.Bounds())
			}
			wImg = imaging.Resize(wImg, screen_width, screen_height, imaging.Lanczos)
			if args.Verbose {
				fmt.Println("Image size after resizing:", wImg.Bounds())
			}
		} else if transform == "center" {
			imgWidth := wImg.Bounds().Max.X
			imgHeight := wImg.Bounds().Max.Y
			if imgWidth > screen_width {
				image_xoffset = (imgWidth - screen_width) / 2
			} else if imgWidth < screen_width {
				screen_xoffset = (screen_width - imgWidth) / 2
			}
			if imgHeight > screen_height {
				image_yoffset = (imgHeight - screen_height) / 2
			} else if imgHeight < screen_height {
				screen_yoffset = (screen_height - imgHeight) / 2
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

	imgWidth := wImg.Bounds().Max.X
	if imgWidth > screen_width {
		imgWidth = screen_width
	}
	imgHeight := wImg.Bounds().Max.Y
	if imgHeight > screen_height {
		imgHeight = screen_height
	}

	if !args.DontClear {
		for i := 0; i < screen_height*screen_width*4; i++ {
			screenPixels[i] = 0
		}
	}

	curPixelBit := (screen_yoffset*screen_width + screen_xoffset) * 4
	if args.Verbose {
		fmt.Println("y from", image_yoffset, "to", image_yoffset+imgHeight, "x from", image_xoffset, "to", image_xoffset+imgWidth)
		fmt.Println("screen y from", screen_yoffset, "screen x from", screen_xoffset)
	}

	for y := image_yoffset; y < image_yoffset+imgHeight; y++ {
		for x := image_xoffset; x < image_xoffset+imgWidth; x++ {
			pixColor := wImg.At(x, y)
			pixColorBits := pixColor.(color.NRGBA)
			screenPixels[curPixelBit] = pixColorBits.R
			curPixelBit++
			screenPixels[curPixelBit] = pixColorBits.G
			curPixelBit++
			screenPixels[curPixelBit] = pixColorBits.B
			curPixelBit++
			screenPixels[curPixelBit] = pixColorBits.A
			curPixelBit++
		}
		if screen_width > imgWidth {
			curPixelBit += (screen_width - imgWidth) * 4
		}
	}
}
