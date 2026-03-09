package common

import (
	"context"
	"fmt"
	"hunter-base/pkg/models"
	"log"
	"runtime"
	"strconv"
	"strings"
	"time"

	cu "github.com/Davincible/chromedp-undetected"
	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

func ParsePrice(raw string) float64 {
	raw = strings.ReplaceAll(raw, "€", "")
	raw = strings.ReplaceAll(raw, "\u00a0", "")
	raw = strings.ReplaceAll(raw, "&nbsp;", "")
	raw = strings.ReplaceAll(raw, "*", "")
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, ",", ".")
	val, _ := strconv.ParseFloat(raw, 64)
	return val
}

func NewProduct(source, id, url string) *models.Product {
	return &models.Product{
		Source:    source,
		ID:        id,
		URL:       url,
		Currency:  "EUR",
		ScrapedAt: time.Now(),
	}
}

func NewUndetectedBrowser(timeout time.Duration) (context.Context, func(), error) {
	opts := []cu.Option{
		cu.WithTimeout(timeout),
	}
	if runtime.GOOS == "linux" {
		opts = append(opts, cu.WithHeadless())
	}

	ctx, cancel, err := cu.New(cu.NewConfig(opts...))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create undetected browser: %w", err)
	}
	return ctx, cancel, nil
}

// ReadyCheck returns true when the page content has loaded (store-specific).
type ReadyCheck func(ctx context.Context) bool

// WaitForCloudflare returns a chromedp.ActionFunc that polls until the Cloudflare
// challenge resolves and the store-specific readyCheck reports the page is loaded.
func WaitForCloudflare(readyCheck ReadyCheck) chromedp.ActionFunc {
	return chromedp.ActionFunc(func(execCtx context.Context) error {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		cfPolls := 0
		for {
			select {
			case <-execCtx.Done():
				if cfPolls > 0 {
					return fmt.Errorf("cloudflare challenge did not resolve after %d polls", cfPolls)
				}
				return execCtx.Err()
			case <-ticker.C:
				var isCF bool
				if err := chromedp.Evaluate(`document.title.includes("Just a moment") || document.title.includes("Cloudflare") || !!document.querySelector('.cf-browser-verification') || !!document.querySelector('#challenge-running') || (document.body && (document.body.innerText.includes("Cloudflare") || document.body.innerText.includes("Ray ID")))`, &isCF).Do(execCtx); err == nil && isCF {
					if cfPolls == 0 {
						log.Println("Cloudflare challenge detected, waiting for auto-resolution...")
					}
					cfPolls++
					continue
				}
				if cfPolls > 0 {
					log.Printf("Cloudflare challenge resolved after %d polls", cfPolls)
				}
				if readyCheck != nil && readyCheck(execCtx) {
					return nil
				}
				if readyCheck == nil {
					return nil
				}
			}
		}
	})
}

func FetchPageHTML(ctx context.Context, url string, readyCheck ReadyCheck) (*goquery.Document, string, error) {
	log.Printf("Navigating to %s", url)
	var html, finalURL string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		WaitForCloudflare(readyCheck),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(`window.location.href`, &finalURL),
		chromedp.OuterHTML(`html`, &html, chromedp.ByQuery),
	)
	if err != nil {
		return nil, "", err
	}
	doc, err := ParseHTML(html)
	if err != nil {
		return nil, "", err
	}
	return doc, finalURL, nil
}

func ParseHTML(html string) (*goquery.Document, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}
	return doc, nil
}

// CheckAvailability determines availability from German-language status text.
// Returns isAvailable and a cleaned label.
func CheckAvailability(text string) (isAvailable bool, label string) {
	label = strings.Join(strings.Fields(text), " ")
	lower := strings.ToLower(label)

	switch {
	case strings.Contains(lower, "sofort lieferbar"), strings.Contains(lower, "auf lager"):
		isAvailable = true
	case strings.Contains(lower, "zur zeit nicht lieferbar"):
		isAvailable = false
	case strings.Contains(lower, "sofortige verfügbarkeitsprüfung"):
		isAvailable = false
	case strings.Contains(lower, "verfügbar"), strings.Contains(lower, "lieferbar"):
		isAvailable = true
	}

	return isAvailable, label
}
