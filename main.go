package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Luzifer/rconfig"
	"github.com/Luzifer/sane"
	"github.com/disintegration/imaging"
	"github.com/jung-kurt/gofpdf"
	log "github.com/sirupsen/logrus"
)

const (
	scanDPI = 300
	pdfDPI  = 150
)

var (
	cfg = struct {
		Listen         string `flag:"listen" default:":3000" description:"Port/IP to listen on"`
		LogLevel       string `flag:"log-level" default:"info" description:"Log level (debug, info, warn, error, fatal)"`
		VersionAndExit bool   `flag:"version" default:"false" description:"Prints current version and exits"`
	}{}

	version = "dev"

	scannerOpts = map[string]interface{}{
		"ald":         true,         // Detect page end for short pages
		"brightness":  25,           // Brighten the image to whiten background
		"br-x":        210.0,        // A4: 210mm
		"br-y":        297.0,        // A4: 297mm
		"buffermode":  "On",         // Read pages fast into scanner buffer
		"mode":        "Color",      // Use color image scans
		"offtimer":    0,            // Don't turn off scanner
		"page-height": 297.0,        // A4: 297mm
		"page-width":  210.0,        // A4: 210mm
		"resolution":  scanDPI,      // Scan with 300dpi for better results
		"source":      "ADF Duplex", // Duplex scan: Both pages at once
		"swdespeck":   2,            // Remove black spots
		"swskip":      10.0,         // If a page is >=10% empty discard it
		"tl-x":        0.0,          // Start the page at 0mm
		"tl-y":        0.0,          // Start the page at 0mm
	}
)

func init() {
	if err := rconfig.ParseAndValidate(&cfg); err != nil {
		log.Fatalf("Unable to parse commandline options: %s", err)
	}

	if cfg.VersionAndExit {
		fmt.Printf("scansnap-go %s\n", version)
		os.Exit(0)
	}

	if l, err := log.ParseLevel(cfg.LogLevel); err != nil {
		log.WithError(err).Fatal("Unable to parse log level")
	} else {
		log.SetLevel(l)
	}
}

func main() {
	http.HandleFunc("/scan.pdf", handleScanRequest)
	http.ListenAndServe(cfg.Listen, nil)
}

func handleScanRequest(res http.ResponseWriter, r *http.Request) {
	start := time.Now()

	pages, err := fetchPages()
	if err != nil {
		log.WithError(err).Error("Unable to fetch pages")
		http.Error(res, "Unable to fetch pages", http.StatusInternalServerError)
		return
	}

	pdf, err := generatePDFFromPages(pages)
	if err != nil {
		log.WithError(err).Error("Unable to generate PDF")
		http.Error(res, "Unable to generate PDF", http.StatusInternalServerError)
		return
	}

	res.Header().Set("X-Generation-Time", time.Since(start).String())
	res.Header().Set("Content-Type", "application/pdf")
	res.Header().Set("Cache-Control", "no-cache")
	io.Copy(res, pdf)
}

func fetchPages() ([]*sane.Image, error) {
	err := sane.Init()
	if err != nil {
		return nil, fmt.Errorf("Unable to initialize SANE: %s", err)
	}

	devs, err := sane.Devices()
	if err != nil {
		return nil, fmt.Errorf("Unable to list devices: %s", err)
	}

	if len(devs) < 1 {
		return nil, fmt.Errorf("No scanners found")
	}

	c, err := sane.Open(devs[0].Name)
	if err != nil {
		return nil, fmt.Errorf("Unable to open scanner: %s", err)
	}

	defer func() {
		c.Cancel()
		c.Close()
		sane.Exit()
	}()

	for name, value := range scannerOpts {
		_, err := c.SetOption(name, value)
		if err != nil {
			return nil, fmt.Errorf("Unable to set option: %s", err)
		}
	}

	return c.ReadAvailableImages()
}

func generatePDFFromPages(pages []*sane.Image) (io.Reader, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	defer pdf.Close()

	for i, p := range pages {
		pdf.AddPage()
		img := new(bytes.Buffer)
		if err := jpeg.Encode(img, reducePageDPI(p), &jpeg.Options{Quality: 95}); err != nil {
			return nil, fmt.Errorf("Unable to encode page %d: %s", i, err)
		}
		imgOpts := gofpdf.ImageOptions{
			ImageType: "jpeg",
			ReadDpi:   true,
		}
		pdf.RegisterImageOptionsReader(fmt.Sprintf("page%d", i), imgOpts, img)
		pdf.ImageOptions(fmt.Sprintf("page%d", i), 0, 0, 210, 0, false, imgOpts, 0, "")
	}

	pdfBuf := new(bytes.Buffer)
	if err := pdf.Output(pdfBuf); err != nil {
		return nil, fmt.Errorf("Unable to render PDF: %s", err)
	}

	return pdfBuf, nil
}

func reducePageDPI(in image.Image) image.Image {
	origW, origH := in.Bounds().Max.X, in.Bounds().Max.Y

	return imaging.Fit(in, origW/(scanDPI/pdfDPI), origH/(scanDPI/pdfDPI), imaging.Lanczos)
}
