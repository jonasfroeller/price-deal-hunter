package apotheke

import (
	"crypto/tls"
	"hunter-base/pkg/models"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

const (
	Source  = "APOTHEKE_AT"
	BaseURL = "https://www.apotheke.at/search.php?query=pzn-"
)

type Scraper struct {
	Collector *colly.Collector
	BaseURL   string
}

func NewScraper() *Scraper {
	c := colly.NewCollector(
		colly.AllowedDomains("www.apotheke.at"),
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		colly.IgnoreRobotsTxt(),
	)
	c.WithTransport(&http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
		ResponseHeaderTimeout: 30 * time.Second,
	})
	c.SetRequestTimeout(30 * time.Second)
	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "de-DE,de;q=0.9,en-US;q=0.8,en;q=0.7")
		r.Headers.Set("Cache-Control", "no-cache")
		r.Headers.Set("Connection", "keep-alive")
		r.Headers.Set("Pragma", "no-cache")
		r.Headers.Set("Sec-Fetch-Dest", "document")
		r.Headers.Set("Sec-Fetch-Mode", "navigate")
		r.Headers.Set("Sec-Fetch-Site", "none")
		r.Headers.Set("Sec-Fetch-User", "?1")
		r.Headers.Set("Upgrade-Insecure-Requests", "1")
	})

	c.OnError(func(r *colly.Response, err error) {
		log.Printf("Request URL: %s failed with response: %v\nError: %v\nBody: %q", r.Request.URL, r.StatusCode, err, string(r.Body))
	})

	return &Scraper{
		Collector: c,
		BaseURL:   BaseURL,
	}
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	product := &models.Product{
		Source:    Source,
		ID:        productID,
		URL:       s.BaseURL + productID,
		Currency:  "EUR",
		ScrapedAt: time.Now(),
	}

	searchURL := product.URL

	s.Collector.OnHTML(".product-card-list", func(e *colly.HTMLElement) {
		// Only take the first product found as it should be the direct hit for the PZN
		if product.Name != "" {
			return
		}

		name := e.ChildText(".product-card__title a")
		if name == "" {
			return
		}

		// apotheke.at URL format is generally https://www.apotheke.at...
		productURL := e.ChildAttr(".product-card__title a", "href")
		if productURL != "" {
			if strings.HasPrefix(productURL, "http") {
				product.URL = productURL
			} else if strings.HasPrefix(productURL, "/") {
				product.URL = "https://www.apotheke.at" + productURL
			}
		}

		product.Name = strings.TrimSpace(name)

		// Regular Price
		priceStr := e.ChildText(".product-card__price--red [aria-hidden='true'] span:first-child")
		if priceStr == "" {
			// Sometime price might not be red if not discounted or different layout
			priceStr = e.ChildText(".product-card__price div[aria-hidden='true'] span:first-child")
		}

		if priceStr != "" {
			priceStr = strings.TrimSpace(priceStr)
			priceStr = strings.ReplaceAll(priceStr, ",", ".")
			if val, err := strconv.ParseFloat(priceStr, 64); err == nil {
				product.Price = val
			}
		}

		// Old/Strikethrough Price (if discounted)
		oldPriceStr := e.ChildText(".product-card__price--cross-out")
		if oldPriceStr != "" {
			oldPriceStr = strings.ReplaceAll(oldPriceStr, "€", "")
			oldPriceStr = strings.ReplaceAll(oldPriceStr, "*", "")
			oldPriceStr = strings.TrimSpace(oldPriceStr)
			oldPriceStr = strings.ReplaceAll(oldPriceStr, ",", ".")
			if val, err := strconv.ParseFloat(oldPriceStr, 64); err == nil {
				product.OldPrice = val
				product.IsDiscounted = true
			}
		}

		// Availability
		availabilityText := e.ChildText(".availability span")
		if availabilityText != "" {
			availLower := strings.ToLower(strings.TrimSpace(availabilityText))
			product.AvailabilityLabel = strings.TrimSpace(availabilityText)

			if strings.Contains(availLower, "sofort lieferbar") || strings.Contains(availLower, "auf lager") {
				product.IsAvailable = true
			} else if strings.Contains(availLower, "zur zeit nicht lieferbar") {
				product.IsAvailable = false
			} else if strings.Contains(availLower, "sofortige verfügbarkeitsprüfung") { // Usually implies it's not strictly guaranteed "in stock" right now.
				product.IsAvailable = false
			} else if strings.Contains(availLower, "lieferbar") {
				// Catch-all for other "lieferbar" phrases that don't match "nicht lieferbar"
				product.IsAvailable = true
			}
		}

		// apoPunkte / Discount Label extensions
		apoPunkteText := e.ChildText(".pdp-buy-box__bonus-text, .product-card__bonus-text")

		if apoPunkteText == "" {
			// Fallback check: scan the whole search list card if the specific class isn't known
			e.ForEachWithBreak(".product-card__info-details div, .product-card__highlight-text li, span", func(_ int, el *colly.HTMLElement) bool {
				text := strings.TrimSpace(el.Text)
				if strings.Contains(strings.ToLower(text), "apopunkte") {
					apoPunkteText = text
					return false
				}
				return true
			})
		}

		if apoPunkteText != "" {
			apoPunkteText = strings.TrimSpace(apoPunkteText)
			// Sometimes it might contain other text, let's just make sure we extract up to "apoPunkte" if it's too long
			if product.DiscountLabel != "" {
				product.DiscountLabel += " | " + apoPunkteText
			} else {
				product.DiscountLabel = apoPunkteText
			}
			product.IsDiscounted = true
		}

		// Price Details (Unit price)
		unitDetails := e.ChildText(".product-card__unit-details")
		if unitDetails != "" {
			product.PriceDetails = strings.TrimSpace(unitDetails)
		}

		// Rating
		ratingStyle := e.ChildAttr(".product-card__rating-foreground", "style")
		if ratingStyle != "" {
			re := regexp.MustCompile(`width:\s*([\d.]+)%`)
			matches := re.FindStringSubmatch(ratingStyle)
			if len(matches) > 1 {
				if percent, err := strconv.ParseFloat(matches[1], 64); err == nil {
					product.Rating = (percent / 100.0) * 5.0
				}
			}
		}

		// Review Count
		reviewCountStr := e.ChildText(".product-card__review-count")
		if reviewCountStr != "" {
			reviewCountStr = strings.Trim(strings.TrimSpace(reviewCountStr), "()")
			if count, err := strconv.Atoi(reviewCountStr); err == nil {
				product.ReviewCount = count
			}
		}
	})

	s.Collector.OnHTML("#product-detail-wrapper", func(e *colly.HTMLElement) {
		// If product is already populated from a search list or another hook, skip it.
		if product.Name != "" {
			return
		}

		name := e.ChildText("h1#pdp-product-title")
		if name == "" {
			return
		}
		product.Name = strings.TrimSpace(name)

		// Current Price
		priceStr := e.ChildText(".product-detail-current-price")
		if priceStr != "" {
			priceStr = strings.ReplaceAll(priceStr, "€", "")
			priceStr = strings.ReplaceAll(priceStr, "*", "")
			priceStr = strings.TrimSpace(priceStr)
			priceStr = strings.ReplaceAll(priceStr, ",", ".")
			if val, err := strconv.ParseFloat(priceStr, 64); err == nil {
				product.Price = val
			}
		}

		// Old Price
		oldPriceStr := e.ChildText(".product-detail-original-price")
		if oldPriceStr != "" {
			oldPriceStr = strings.ReplaceAll(oldPriceStr, "€", "")
			oldPriceStr = strings.ReplaceAll(oldPriceStr, "*", "")
			oldPriceStr = strings.TrimSpace(oldPriceStr)
			oldPriceStr = strings.ReplaceAll(oldPriceStr, ",", ".")
			if val, err := strconv.ParseFloat(oldPriceStr, 64); err == nil {
				product.OldPrice = val
				product.IsDiscounted = true
			}
		}

		// Availability
		availabilityText := e.ChildText(".pdp-buy-box__status-text")
		if availabilityText != "" {
			// clean up internal text splitting (e.g. "zur Zeit nicht lieferbar" in nested spans)
			availClean := strings.Join(strings.Fields(availabilityText), " ")
			availLower := strings.ToLower(availClean)
			product.AvailabilityLabel = availClean

			if strings.Contains(availLower, "sofort lieferbar") || strings.Contains(availLower, "auf lager") {
				product.IsAvailable = true
			} else if strings.Contains(availLower, "zur zeit nicht lieferbar") {
				product.IsAvailable = false
			} else if strings.Contains(availLower, "sofortige verfügbarkeitsprüfung") {
				product.IsAvailable = false
			} else if strings.Contains(availLower, "lieferbar") {
				product.IsAvailable = true
			}
		}

		// apoPunkte / Discount Label
		apoPunkteText := e.ChildText(".pdp-buy-box__bonus-text")
		if apoPunkteText != "" {
			apoPunkteText = strings.TrimSpace(apoPunkteText)
			if product.DiscountLabel != "" {
				product.DiscountLabel += " | " + apoPunkteText
			} else {
				product.DiscountLabel = apoPunkteText
			}
			product.IsDiscounted = true
		}

		// Rating & Reviews
		scoreStr := e.ChildText(".pdp-reviews__score")
		if scoreStr != "" {
			scoreStr = strings.ReplaceAll(scoreStr, ",", ".")
			if val, err := strconv.ParseFloat(strings.TrimSpace(scoreStr), 64); err == nil {
				product.Rating = val
			}
		}

		reviewCountStr := e.ChildText(".pdp-reviews__count")
		if reviewCountStr == "" {
			reviewCountStr = e.ChildText(".pdp-buy-box__rating-count")
		}
		if reviewCountStr != "" {
			re := regexp.MustCompile(`\d+`)
			matches := re.FindStringSubmatch(reviewCountStr)
			if len(matches) > 0 {
				if count, err := strconv.Atoi(matches[0]); err == nil {
					product.ReviewCount = count
				}
			}
		}
	})

	// Sometimes the search page doesn't have all details.
	s.Collector.OnHTML(".product-card__title a[href]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		e.Request.Visit(link)
	})

	log.Printf("Navigating to search URL %s", searchURL)
	err := s.Collector.Visit(searchURL)
	if err != nil {
		return nil, err
	}

	if product.Name == "" {
		return nil, models.ErrProductNotFound
	}

	return product, nil
}
