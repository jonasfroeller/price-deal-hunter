package hofer

import (
	"context"
	"encoding/json"
	"fmt"
	"hunter-base/pkg/models"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	Source  = "HOFER"
	BaseURL = "https://www.hofer.at/de/p."
)

type Scraper struct{}

func NewScraper() *Scraper {
	return &Scraper{}
}

type JSONLDProduct struct {
	Type        string `json:"@type"`
	Name        string `json:"name"`
	Image       string `json:"image"`
	Description string `json:"description"`
	Sku         string `json:"sku"`
	Offers      struct {
		Type          string          `json:"@type"`
		Availability  string          `json:"availability"`
		Price         json.RawMessage `json:"price"` // Can be string or number
		PriceCurrency string          `json:"priceCurrency"`
		Url           string          `json:"url"`
	} `json:"offers"`
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	// Hofer URL construction: https://www.hofer.at/de/p.{id}.html
	productURL := fmt.Sprintf("%s%s.html", BaseURL, productID)

	product := &models.Product{
		Source:    Source,
		ID:        productID,
		URL:       productURL,
		Currency:  "EUR",
		ScrapedAt: time.Now(),
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"),
		chromedp.WindowSize(1920, 1080),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	scrapeCtx, cancelScrape := context.WithTimeout(ctx, 45*time.Second)
	defer cancelScrape()

	var jsonLDContent string
	var priceNowStr string

	log.Printf("[HOFER] Navigating to %s", product.URL)

	err := chromedp.Run(scrapeCtx,
		chromedp.Navigate(product.URL),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),

		// Extract JSON-LD
		chromedp.Evaluate(`
			(function() {
				const scripts = document.querySelectorAll('script[type="application/ld+json"]');
				for (const script of scripts) {
					if (script.innerText.includes('"@type": "Product"') || script.innerText.includes('"@type":"Product"')) {
						return script.innerText;
					}
				}
				return "";
			})()
		`, &jsonLDContent),

		// Extract Price label directly if JSON-LD fails or as backup
		chromedp.Evaluate(`
			(function() {
				let el = document.querySelector(".pdp_price__now") || document.querySelector(".at-productprice_lbl");
				return el ? el.innerText : "";
			})()
		`, &priceNowStr),
	)

	if err != nil {
		return nil, fmt.Errorf("chromedp execution failed: %w", err)
	}

	// 1. Try JSON-LD first
	if jsonLDContent != "" {
		var ld ProductJSONLD
		if err := json.Unmarshal([]byte(jsonLDContent), &ld); err == nil {
			product.Name = strings.TrimSpace(ld.Name)
			if ld.Offers.PriceCurrency != "" {
				product.Currency = ld.Offers.PriceCurrency
			}

			// Parse Price (handle string or number)
			var priceStr string
			if len(ld.Offers.Price) > 0 {
				// Try unquote first if it's a string
				if err := json.Unmarshal(ld.Offers.Price, &priceStr); err != nil {
					priceStr = string(ld.Offers.Price)
				}
			}

			// Clean price string
			priceStr = strings.Trim(priceStr, `"'`)
			if val, err := strconv.ParseFloat(priceStr, 64); err == nil {
				product.Price = val
			}

			// Availability
			avail := strings.ToLower(ld.Offers.Availability)
			if strings.Contains(avail, "instock") || avail == "in stock" {
				product.IsAvailable = true
				product.AvailabilityLabel = "Available"
			} else {
				product.IsAvailable = false
				product.AvailabilityLabel = ld.Offers.Availability
			}

			// Update URL if redirected/different in JSON
			if ld.Offers.Url != "" {
				if _, err := url.Parse(ld.Offers.Url); err == nil {
					product.URL = ld.Offers.Url
				}
			}
		} else {
			log.Printf("[HOFER] Failed to parse JSON-LD: %v", err)
		}
	}

	// 2. Fallback to HTML selectors
	if product.Name == "" {
		var h1 string
		if err := chromedp.Run(scrapeCtx, chromedp.Evaluate(`document.querySelector("h1")?.innerText || ""`, &h1)); err == nil {
			product.Name = strings.TrimSpace(h1)
		}
	}

	if product.Price == 0 && priceNowStr != "" {
		// Clean "€ 0,99" => 0.99
		cleaned := strings.ReplaceAll(priceNowStr, "€", "")
		cleaned = strings.ReplaceAll(cleaned, ",", ".")
		cleaned = strings.TrimSpace(cleaned)

		// Remove hidden chars
		cleaned = strings.Map(func(r rune) rune {
			if r < 32 || r > 126 {
				return -1
			}
			return r
		}, cleaned)

		if val, err := strconv.ParseFloat(cleaned, 64); err == nil {
			product.Price = val

			// Assume available if price is shown (TODO: improve logic here)
			if !product.IsAvailable && product.AvailabilityLabel == "" {
				product.IsAvailable = true
			}
		}
	}

	if product.Name == "" {
		return nil, models.ErrProductNotFound
	}

	return product, nil
}

type ProductJSONLD struct {
	Type   string `json:"@type"`
	Name   string `json:"name"`
	Offers struct {
		Price         json.RawMessage `json:"price"`
		PriceCurrency string          `json:"priceCurrency"`
		Availability  string          `json:"availability"`
		Url           string          `json:"url"`
	} `json:"offers"`
}
