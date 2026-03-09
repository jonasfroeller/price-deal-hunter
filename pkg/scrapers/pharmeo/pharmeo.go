package pharmeo

import (
	"context"
	"fmt"
	"hunter-base/pkg/models"
	"log"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	cu "github.com/Davincible/chromedp-undetected"
	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

const (
	Source  = "PHARMEO_AT"
	BaseURL = "https://www.pharmeo.at"
)

type Scraper struct {
	BaseURL string
}

func NewScraper() *Scraper {
	return &Scraper{
		BaseURL: BaseURL,
	}
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	product := &models.Product{
		Source:    Source,
		ID:        productID,
		URL:       s.BaseURL,
		Currency:  "EUR",
		ScrapedAt: time.Now(),
	}

	opts := []cu.Option{
		cu.WithTimeout(120 * time.Second),
	}
	if runtime.GOOS == "linux" {
		opts = append(opts, cu.WithHeadless())
	}

	ctx, cancel, err := cu.New(cu.NewConfig(opts...))
	if err != nil {
		return nil, fmt.Errorf("failed to create undetected browser: %w", err)
	}
	defer cancel()

	log.Printf("Navigating to %s", s.BaseURL)

	var html, finalURL string
	err = chromedp.Run(ctx,
		chromedp.Navigate(s.BaseURL),
		chromedp.WaitVisible(`input#q`, chromedp.ByQuery),
		chromedp.Clear(`input#q`, chromedp.ByQuery),
		chromedp.SendKeys(`input#q`, productID+"\n", chromedp.ByQuery),
		chromedp.ActionFunc(func(execCtx context.Context) error {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			polls := 0
			for {
				select {
				case <-execCtx.Done():
					return fmt.Errorf("timed out waiting for product detail page after %d polls", polls)
				case <-ticker.C:
					polls++
					var hasDetail bool
					if err := chromedp.Evaluate(
						`!!document.querySelector(".product-detail-information") || !!document.querySelector(".product-detail-title")`,
						&hasDetail,
					).Do(execCtx); err == nil && hasDetail {
						return nil
					}

					// Check if we landed on a search results list instead
					var hasSearchResults bool
					if err := chromedp.Evaluate(
						`!!document.querySelector(".product-list") || !!document.querySelector(".search-result")`,
						&hasSearchResults,
					).Do(execCtx); err == nil && hasSearchResults {
						// Click the first product link
						var firstLink string
						if err := chromedp.Evaluate(
							`(function() { var a = document.querySelector('.product-list a[href], .search-result a[href]'); return a ? a.href : ''; })()`,
							&firstLink,
						).Do(execCtx); err == nil && firstLink != "" {
							log.Printf("Search returned a list, navigating to first result: %s", firstLink)
							if err := chromedp.Navigate(firstLink).Do(execCtx); err != nil {
								return fmt.Errorf("failed to navigate to first result: %w", err)
							}
							continue
						}
					}

					if polls > 60 {
						return fmt.Errorf("product detail page did not load after %d polls", polls)
					}
				}
			}
		}),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(`window.location.href`, &finalURL),
		chromedp.OuterHTML(`html`, &html, chromedp.ByQuery),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch product page: %w", err)
	}

	product.URL = finalURL
	log.Printf("Landed on %s", finalURL)

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	parseDetailPage(doc, product)

	if product.Name == "" {
		return nil, models.ErrProductNotFound
	}

	return product, nil
}

func parseDetailPage(doc *goquery.Document, product *models.Product) {
	sel := doc.Find(".product-detail-information")
	if sel.Length() == 0 {
		return
	}

	// Name
	name := sel.Find("h1.product-detail-title").Text()
	if name == "" {
		return
	}
	product.Name = strings.TrimSpace(name)

	// Price
	priceStr := sel.Find(".sale-price").Text()
	if priceStr != "" {
		product.Price = parsePrice(priceStr)
	}

	// Reference price (UVP) = old price => marks as discounted
	refPriceStr := sel.Find(".reference-price-amount").Text()
	if refPriceStr != "" {
		if oldPrice := parsePrice(refPriceStr); oldPrice > 0 && oldPrice > product.Price {
			product.OldPrice = oldPrice
			product.IsDiscounted = true
		}
	}

	// Price details (unit price)
	priceDetails := sel.Find(".product-detail-product-info").Text()
	if priceDetails != "" {
		priceDetails = strings.Join(strings.Fields(priceDetails), " ")
		priceDetails = strings.TrimSpace(priceDetails)
		product.PriceDetails = priceDetails
	}

	// Availability
	availText := sel.Find(".product-detail-availability").Text()
	if availText != "" {
		availClean := strings.Join(strings.Fields(availText), " ")
		availLower := strings.ToLower(availClean)
		product.AvailabilityLabel = strings.TrimSpace(availClean)

		if strings.Contains(availLower, "verfügbar") || strings.Contains(availLower, "lieferbar") || strings.Contains(availLower, "auf lager") {
			product.IsAvailable = true
		}
	}

	// Rating (count filled stars)
	starItems := sel.Find(".product-rating-summary-stars li")
	if starItems.Length() > 0 {
		filledCount := 0
		starItems.Each(func(_ int, li *goquery.Selection) {
			href, exists := li.Find("use").Attr("xlink:href")
			if exists && !strings.Contains(href, "outline") {
				filledCount++
			}
		})
		if filledCount > 0 {
			product.Rating = float64(filledCount)
		}
	}

	// Attributes (PZN, Darreichungsform, etc.)
	sel.Find(".product-detail-attributes .row .col-6.col-md-5, .product-detail-attributes .row .col-6.col-lg-4").Each(func(i int, attrLabel *goquery.Selection) {
		labelText := strings.TrimSpace(attrLabel.Find(".product-detail-attributes__attribute").Text())
		valueEl := attrLabel.Next()
		valueText := strings.TrimSpace(valueEl.Find(".product-detail-attributes__attribute-value").Text())

		if strings.Contains(labelText, "PZN") && valueText != "" && valueText != product.ID {
			log.Printf("PZN mismatch: expected %s, got %s", product.ID, valueText)
		}
	})

	// Discount: check if there are variant badges with discount percentages for the active variant
	activeVariant := sel.Find(".product-variants-item.active")
	if activeVariant.Length() > 0 {
		badge := activeVariant.Find(".product-variants-item-badge .badge-content").Text()
		if badge != "" {
			badge = strings.TrimSpace(badge)
			re := regexp.MustCompile(`-(\d+)%`)
			if matches := re.FindStringSubmatch(badge); len(matches) > 1 {
				if discount, err := strconv.Atoi(matches[1]); err == nil && discount > 0 {
					product.IsDiscounted = true
					product.DiscountLabel = fmt.Sprintf("-%d%%", discount)
				}
			}
		}
	}
}

func parsePrice(raw string) float64 {
	raw = strings.ReplaceAll(raw, "€", "")
	raw = strings.ReplaceAll(raw, "*", "")
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, ",", ".")
	val, _ := strconv.ParseFloat(raw, 64)
	return val
}
