package provider

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestLogoDecodesSVGAndWindowsICOPNG(t *testing.T) {
	svg := []byte(`<svg width="100" height="100" xmlns="http://www.w3.org/2000/svg"><path fill="#00aaff" d="M0 0h100v100H0z"/></svg>`)
	if img := decodeLogo(svg); img == nil || img.Bounds().Dx() != 64 {
		t.Fatal("official SVG favicon was not rendered")
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	ico := make([]byte, 22)
	binary.LittleEndian.PutUint16(ico[2:], 1)
	binary.LittleEndian.PutUint16(ico[4:], 1)
	binary.LittleEndian.PutUint32(ico[14:], uint32(pngData.Len()))
	binary.LittleEndian.PutUint32(ico[18:], 22)
	ico = append(ico, pngData.Bytes()...)
	if decodeLogo(ico) == nil {
		t.Fatal("Windows ICO with embedded PNG was not decoded")
	}
	binary.LittleEndian.PutUint32(ico[18:], 0xffffffff)
	if decodeLogo(ico) != nil {
		t.Fatal("accepted an invalid ICO offset")
	}
}

type logoTransport func(*http.Request) (*http.Response, error)

func (f logoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLogoFallsBackToOfficialWebsiteWhenFaviconServiceHasNoEntry(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/s2/favicons":
			http.NotFound(w, r)
		case "/":
			fmt.Fprint(w, `<html><head><link rel="icon" href="/mark.svg"></head></html>`)
		case "/mark.svg":
			fmt.Fprint(w, `<svg width="32" height="32" xmlns="http://www.w3.org/2000/svg"><path fill="red" d="M0 0h32v32H0z"/></svg>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := server.Client()
	transport := client.Transport
	target, _ := url.Parse(server.URL)
	client.Transport = logoTransport(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Host = target.Host
		clone.URL = &u
		return transport.RoundTrip(clone)
	})
	if lookupLogo(context.Background(), client, "example.com") == nil {
		t.Fatal("official website's SVG fallback did not load")
	}
}

func TestProviderDisplayNameRetainsBrand(t *testing.T) {
	if got := DisplayName("Earthlink Telecommunications Equipment Trading & Services DMCC"); got != "Earthlink" {
		t.Fatalf("got %q", got)
	}
	if got := DisplayName("Al-Jazeera Al-Arabiya Company for Communication and Internet LTD"); got != "Al-Jazeera Al-Arabiya" {
		t.Fatalf("got %q", got)
	}
}
