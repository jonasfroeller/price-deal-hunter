package pharmeo

import (
	"context"
	"fmt"
	"hunter-base/pkg/models"
	"hunter-base/pkg/scrapers/common"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	product := common.NewProduct(Source, productID, s.BaseURL)

	ctx, cancel, err := common.NewUndetectedBrowser(120 * time.Second)
	if err != nil {
		return nil, err
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

					var hasSearchResults bool
					if err := chromedp.Evaluate(
						`!!document.querySelector(".product-list") || !!document.querySelector(".search-result")`,
						&hasSearchResults,
					).Do(execCtx); err == nil && hasSearchResults {
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

	doc, err := common.ParseHTML(html)
	if err != nil {
		return nil, err
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

	name := sel.Find("h1.product-detail-title").Text()
	if name == "" {
		return
	}
	product.Name = strings.TrimSpace(name)

	priceStr := sel.Find(".sale-price").Text()
	if priceStr != "" {
		product.Price = common.ParsePrice(priceStr)
	}

	refPriceStr := sel.Find(".reference-price-amount").Text()
	if refPriceStr != "" {
		if oldPrice := common.ParsePrice(refPriceStr); oldPrice > 0 && oldPrice > product.Price {
			product.OldPrice = oldPrice
			product.IsDiscounted = true
		}
	}

	priceDetails := sel.Find(".product-detail-product-info").Text()
	if priceDetails != "" {
		priceDetails = strings.Join(strings.Fields(priceDetails), " ")
		product.PriceDetails = strings.TrimSpace(priceDetails)
	}

	availText := sel.Find(".product-detail-availability").Text()
	if availText != "" {
		product.IsAvailable, product.AvailabilityLabel = common.CheckAvailability(availText)
	}

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

	sel.Find(".product-detail-attributes .row .col-6.col-md-5, .product-detail-attributes .row .col-6.col-lg-4").Each(func(i int, attrLabel *goquery.Selection) {
		labelText := strings.TrimSpace(attrLabel.Find(".product-detail-attributes__attribute").Text())
		valueEl := attrLabel.Next()
		valueText := strings.TrimSpace(valueEl.Find(".product-detail-attributes__attribute-value").Text())

		if strings.Contains(labelText, "PZN") && valueText != "" && valueText != product.ID {
			log.Printf("PZN mismatch: expected %s, got %s", product.ID, valueText)
		}
	})

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
