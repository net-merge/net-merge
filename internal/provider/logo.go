package provider

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/net/html"
)

func lookupLogo(ctx context.Context, client *http.Client, domain string) image.Image {
	if data, err := getLogoData(ctx, client, "https://www.google.com/s2/favicons?sz=64&domain="+url.QueryEscape(domain)); err == nil {
		if img := decodeLogo(data); img != nil {
			return img
		}
	}
	// Not every ISP is indexed by favicon services. Inspect only its official
	// HTTPS home page and icon links. clientFor rejects private/loopback targets.
	base, _ := url.Parse("https://" + domain + "/")
	if page, err := getLogoData(ctx, client, base.String()); err == nil {
		if doc, err := html.Parse(bytes.NewReader(page)); err == nil {
			candidates := []string{}
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode && (n.Data == "link" || n.Data == "img") {
					attrs := map[string]string{}
					for _, a := range n.Attr {
						attrs[a.Key] = a.Val
					}
					if n.Data == "link" && strings.Contains(strings.ToLower(attrs["rel"]), "icon") {
						candidates = append(candidates, attrs["href"])
					}
					if n.Data == "img" && strings.Contains(strings.ToLower(attrs["alt"]), "logo") {
						candidates = append(candidates, attrs["src"])
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)
			seen := map[string]bool{}
			for _, candidate := range candidates {
				if len(seen) >= 4 {
					break
				}
				ref, err := url.Parse(candidate)
				if err != nil {
					continue
				}
				endpoint := base.ResolveReference(ref)
				if endpoint.Scheme != "https" || endpoint.User != nil || (endpoint.Port() != "" && endpoint.Port() != "443") || !validDomain(endpoint.Hostname()) {
					continue
				}
				if seen[endpoint.String()] {
					continue
				}
				seen[endpoint.String()] = true
				if data, err := getLogoData(ctx, client, endpoint.String()); err == nil {
					if img := decodeLogo(data); img != nil {
						return img
					}
				}
			}
		}
	}
	if data, err := getLogoData(ctx, client, base.ResolveReference(&url.URL{Path: "/favicon.ico"}).String()); err == nil {
		return decodeLogo(data)
	}
	return nil
}

func getLogoData(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "net-merge/provider")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider image: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (512<<10)+1))
	if len(data) > 512<<10 {
		return nil, fmt.Errorf("provider image too large")
	}
	return data, err
}

func decodeLogo(data []byte) (result image.Image) {
	defer func() {
		if recover() != nil {
			result = nil
		}
	}()
	if len(data) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.Contains(trimmed[:min(512, len(trimmed))], []byte("<svg")) {
		if len(data) > 128<<10 {
			return nil
		}
		icon, err := oksvg.ReadIconStream(bytes.NewReader(data))
		if err != nil || icon.ViewBox.W <= 0 || icon.ViewBox.H <= 0 {
			return nil
		}
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		scale := min(64/icon.ViewBox.W, 64/icon.ViewBox.H)
		width, height := icon.ViewBox.W*scale, icon.ViewBox.H*scale
		icon.SetTarget((64-width)/2, (64-height)/2, width, height)
		icon.Draw(rasterx.NewDasher(64, 64, rasterx.NewScannerGV(64, 64, img, img.Bounds())), 1)
		return img
	}
	// Modern Windows .ico files usually contain PNGs. Support classic 32-bit
	// BGRA icons too, without asking Windows to load untrusted resources.
	if len(data) >= 6 && binary.LittleEndian.Uint16(data) == 0 && binary.LittleEndian.Uint16(data[2:]) == 1 {
		count := int(binary.LittleEndian.Uint16(data[4:]))
		for i := 0; i < count && i < 32; i++ {
			offset := 6 + i*16
			if offset+16 > len(data) {
				break
			}
			size := int(binary.LittleEndian.Uint32(data[offset+8:]))
			start := int(binary.LittleEndian.Uint32(data[offset+12:]))
			if start < 6 || size < 1 || start > len(data) || size > len(data)-start {
				continue
			}
			entry := data[start : start+size]
			if img := decodeRaster(entry); img != nil {
				return img
			}
			if len(entry) >= 40 && binary.LittleEndian.Uint32(entry) == 40 && binary.LittleEndian.Uint16(entry[14:]) == 32 {
				width, height := int(int32(binary.LittleEndian.Uint32(entry[4:]))), int(int32(binary.LittleEndian.Uint32(entry[8:])))/2
				if width < 1 || height < 1 || width > 256 || height > 256 || 40+width*height*4 > len(entry) {
					continue
				}
				img := image.NewNRGBA(image.Rect(0, 0, width, height))
				for y := 0; y < height; y++ {
					for x := 0; x < width; x++ {
						i := 40 + ((height-1-y)*width+x)*4
						img.SetNRGBA(x, y, color.NRGBA{R: entry[i+2], G: entry[i+1], B: entry[i], A: entry[i+3]})
					}
				}
				return smallLogo(img)
			}
		}
		return nil
	}
	return decodeRaster(data)
}

func decodeRaster(data []byte) image.Image {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || cfg.Width*cfg.Height > 2_000_000 {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return smallLogo(img)
}
func smallLogo(img image.Image) image.Image {
	b := img.Bounds()
	scale := min(64/float64(b.Dx()), 64/float64(b.Dy()))
	width, height := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	out := image.NewRGBA(image.Rect(0, 0, 64, 64))
	left, top := (64-width)/2, (64-height)/2
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			out.Set(left+x, top+y, img.At(b.Min.X+x*b.Dx()/width, b.Min.Y+y*b.Dy()/height))
		}
	}
	return out
}

// DisplayName trims common legal/descriptive suffixes while Info.Name retains
// the full provider identity for diagnostics and future detail views.
func DisplayName(name string) string {
	for _, suffix := range []string{" Telecommunications", " Telecommunication", " Company for ", " Communications", " Equipment Trading", " Internet Services", " Co.,", " LLC", " LTD"} {
		if index := strings.Index(strings.ToLower(name), strings.ToLower(suffix)); index >= 3 {
			name = name[:index]
		}
	}
	return strings.TrimSpace(name)
}
