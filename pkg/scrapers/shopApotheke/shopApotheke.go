package shopApotheke

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
	Source  = "SHOP_APOTHEKE_AT"
	BaseURL = "https://www.shop-apotheke.at"
)

type Scraper struct {
	BaseURL string
}

func NewScraper() *Scraper {
	return &Scraper{
		BaseURL: BaseURL,
	}
}

func buildProductURLs(baseURL, pzn string) []string {
	trimmed := strings.TrimLeft(pzn, "0")
	if trimmed == "" {
		trimmed = "0"
	}

	hasLeadingZeros := trimmed != pzn

	if hasLeadingZeros {
		return []string{
			baseURL + "/arzneimittel/D" + trimmed + "/index.htm",
			baseURL + "/arzneimittel/A" + pzn + "/index.htm",
		}
	}
	return []string{
		baseURL + "/arzneimittel/A" + pzn + "/index.htm",
		baseURL + "/arzneimittel/D" + trimmed + "/index.htm",
	}
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	candidateURLs := buildProductURLs(s.BaseURL, productID)

	product := common.NewProduct(Source, productID, candidateURLs[0])

	ctx, cancel, err := common.NewUndetectedBrowser(120 * time.Second)
	if err != nil {
		return nil, err
	}
	defer cancel()

	for _, candidateURL := range candidateURLs {
		log.Printf("Trying URL: %s", candidateURL)
		html, finalURL, err := navigateToProduct(ctx, candidateURL)
		if err == errNotFound {
			log.Printf("Not found at %s, trying next", candidateURL)
			continue
		}
		if err != nil {
			log.Printf("Failed at %s: %v, trying next", candidateURL, err)
			continue
		}
		return buildProduct(html, finalURL, product)
	}

	log.Printf("URL attempts failed, falling back to search for PZN %s", productID)
	html, finalURL, err := searchForProduct(ctx, s.BaseURL, productID)
	if err != nil {
		if err == errNotFound {
			return nil, models.ErrProductNotFound
		}
		return nil, fmt.Errorf("search fallback failed: %w", err)
	}
	return buildProduct(html, finalURL, product)
}

var errNotFound = fmt.Errorf("product not found on page")

func navigateToProduct(ctx context.Context, url string) (string, string, error) {
	var html, finalURL string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.ActionFunc(func(execCtx context.Context) error {
			return waitForProductOrError(execCtx)
		}),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(`window.location.href`, &finalURL),
		chromedp.OuterHTML(`html`, &html, chromedp.ByQuery),
	)
	if err == errNotFound || err == models.ErrProductNotFound {
		return "", "", errNotFound
	}
	if err != nil {
		return "", "", err
	}
	return html, finalURL, nil
}

func searchForProduct(ctx context.Context, baseURL, pzn string) (string, string, error) {
	searchURL := baseURL + "/search.htm?q=" + pzn
	log.Printf("Searching: %s", searchURL)

	var html, finalURL string
	err := chromedp.Run(ctx,
		chromedp.Navigate(searchURL),
		chromedp.ActionFunc(func(execCtx context.Context) error {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			polls := 0
			for {
				select {
				case <-execCtx.Done():
					return fmt.Errorf("timed out waiting for search results after %d polls", polls)
				case <-ticker.C:
					polls++

					var hasProduct bool
					if err := chromedp.Evaluate(
						`!!document.querySelector('[data-qa-id="product-title"]')`,
						&hasProduct,
					).Do(execCtx); err == nil && hasProduct {
						return nil
					}

					var firstProductHref string
					if err := chromedp.Evaluate(
						`(function() { var a = document.querySelector('[data-qa-id="serp-result-item-title"]'); return a ? a.href : ''; })()`,
						&firstProductHref,
					).Do(execCtx); err == nil && firstProductHref != "" {
						log.Printf("Search found result, navigating to: %s", firstProductHref)
						if err := chromedp.Navigate(firstProductHref).Do(execCtx); err != nil {
							return fmt.Errorf("failed to navigate to search result: %w", err)
						}
						return waitForProductOrError(execCtx)
					}

					var noResults bool
					if err := chromedp.Evaluate(
						`!!document.querySelector('[data-qa-id="search-no-results"]') || (document.querySelectorAll('[data-qa-id="result-list-entry"]').length === 0 && document.readyState === 'complete')`,
						&noResults,
					).Do(execCtx); err == nil && noResults && polls > 10 {
						return errNotFound
					}

					if polls > 60 {
						return fmt.Errorf("search did not resolve after %d polls", polls)
					}
				}
			}
		}),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(`window.location.href`, &finalURL),
		chromedp.OuterHTML(`html`, &html, chromedp.ByQuery),
	)
	if err == errNotFound {
		return "", "", errNotFound
	}
	if err != nil {
		return "", "", err
	}
	return html, finalURL, nil
}

func waitForProductOrError(execCtx context.Context) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	polls := 0
	for {
		select {
		case <-execCtx.Done():
			return fmt.Errorf("timed out waiting for product page after %d polls", polls)
		case <-ticker.C:
			polls++
			var hasContent bool
			if err := chromedp.Evaluate(
				`!!document.querySelector('[data-qa-id="product-title"]') || !!document.querySelector('[data-qa-id="product-details-page"]')`,
				&hasContent,
			).Do(execCtx); err == nil && hasContent {
				return nil
			}

			var is404 bool
			if err := chromedp.Evaluate(
				`document.title.includes("404") || document.title.includes("nicht gefunden") || !!document.querySelector('[data-qa-id="error-page"]') || !!document.querySelector('h1')?.textContent?.includes('Entschuldigung')`,
				&is404,
			).Do(execCtx); err == nil && is404 {
				return errNotFound
			}

			if polls > 30 {
				return fmt.Errorf("product page did not load after %d polls", polls)
			}
		}
	}
}

func buildProduct(html, finalURL string, product *models.Product) (*models.Product, error) {
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
	page := doc.Find(`[data-qa-id="product-details-page"]`)
	if page.Length() == 0 {
		page = doc.Selection
	}

	name := page.Find(`[data-qa-id="product-title"]`).Text()
	if name == "" {
		return
	}
	product.Name = strings.TrimSpace(name)

	filledStars := page.Find(`[data-qa-id="active-rating-star"]`).Length()
	if filledStars > 0 {
		product.Rating = float64(filledStars)
	}

	reviewText := page.Find(`[data-qa-id="number-of-ratings-text"]`).Text()
	if reviewText != "" {
		re := regexp.MustCompile(`\d+`)
		if matches := re.FindString(reviewText); matches != "" {
			if count, err := strconv.Atoi(matches); err == nil {
				product.ReviewCount = count
			}
		}
	}

	page.Find(`[data-qa-id="product-variants"]`).Each(func(_ int, li *goquery.Selection) {
		v := parseVariant(li)

		isActive := li.Children().Filter("div").Length() > 0

		if isActive {
			product.Price = v.Price
			if v.OldPrice > 0 && v.OldPrice > v.Price {
				product.OldPrice = v.OldPrice
				product.IsDiscounted = true
			}
			if v.DiscountLabel != "" {
				product.DiscountLabel = v.DiscountLabel
				product.IsDiscounted = true
			}
			product.PriceDetails = v.PriceDetails
		} else {
			product.Variants = append(product.Variants, v)
		}
	})

	if product.Price == 0 {
		priceText := page.Find(`[data-qa-id="product-page-variant-details__display-price"]`).First().Text()
		if priceText != "" {
			product.Price = common.ParsePrice(priceText)
		}
	}

	availText := page.Find(`[data-qa-id="product-status-qa-id"]`).Text()
	if availText != "" {
		product.IsAvailable, product.AvailabilityLabel = common.CheckAvailability(availText)
	}
}

func parseVariant(li *goquery.Selection) models.Variant {
	v := models.Variant{}

	pkgSize := li.Find(`[data-qa-id="product-attribute-package_size"]`).Text()
	v.Name = strings.TrimSpace(pkgSize)

	priceText := li.Find(`[data-qa-id="product-page-variant-details__display-price"]`).Text()
	if priceText != "" {
		v.Price = common.ParsePrice(priceText)
	}

	oldPriceText := li.Find(`[data-qa-id="product-old-price"]`).Text()
	if oldPriceText != "" {
		if oldPrice := common.ParsePrice(oldPriceText); oldPrice > 0 && oldPrice > v.Price {
			v.OldPrice = oldPrice
			v.IsDiscounted = true
		}
	}

	li.Find(".bg-light-tertiary").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		re := regexp.MustCompile(`^-\d+%$`)
		if re.MatchString(text) && v.DiscountLabel == "" {
			v.DiscountLabel = text
			v.IsDiscounted = true
		}
	})

	unitPriceEl := li.Find(`[data-qa-id="product-attribute-package_size"]`).Parent().Find("div").Last()
	if unitPriceEl.Length() > 0 {
		unitText := strings.TrimSpace(unitPriceEl.Text())
		if strings.Contains(unitText, "/") {
			v.PriceDetails = v.Name + " | " + unitText
		} else {
			v.PriceDetails = v.Name
		}
	}

	if link := li.Find(`[data-qa-id="product-variant"]`); link.Length() > 0 {
		href, exists := link.Attr("href")
		if exists && href != "" {
			if strings.HasPrefix(href, "/") {
				v.URL = BaseURL + href
			} else {
				v.URL = href
			}
		}
	}

	return v
}
