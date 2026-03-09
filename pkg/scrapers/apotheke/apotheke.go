package apotheke

import (
	"hunter-base/pkg/models"
	"hunter-base/pkg/scrapers/common"
	"log"
	"regexp"
	"strconv"
	"strings"

	"context"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

const (
	Source  = "APOTHEKE_AT"
	BaseURL = "https://www.apotheke.at/search.php?query=pzn-"
)

type Scraper struct {
	BaseURL string
}

func NewScraper() *Scraper {
	return &Scraper{
		BaseURL: BaseURL,
	}
}

func apothekeReadyCheck(execCtx context.Context) bool {
	var hasResults bool
	if err := chromedp.Evaluate(`!!(document.querySelector(".search-result-header") || document.querySelector("#product-detail-wrapper") || document.querySelector(".product-card") || document.querySelector(".product-card-list"))`, &hasResults).Do(execCtx); err == nil && hasResults {
		return true
	}
	return false
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	product := common.NewProduct(Source, productID, s.BaseURL+productID)
	searchURL := product.URL

	ctx, cancel, err := common.NewUndetectedBrowser(120 * time.Second)
	if err != nil {
		return nil, err
	}
	defer cancel()

	searchDoc, _, err := common.FetchPageHTML(ctx, searchURL, apothekeReadyCheck)
	if err != nil {
		return nil, err
	}

	foundLink := parseSearchCard(searchDoc, product)

	if product.Name != "" && foundLink != "" && foundLink != searchURL {
		pdpDoc, _, pdpErr := common.FetchPageHTML(ctx, foundLink, apothekeReadyCheck)
		if pdpErr == nil {
			parsePDP(pdpDoc, product)
		} else {
			log.Printf("Failed to fetch PDP %s: %v", foundLink, pdpErr)
		}
	} else {
		if product.Name == "" {
			parsePDP(searchDoc, product)
		}
	}

	if product.Name == "" {
		return nil, models.ErrProductNotFound
	}

	return product, nil
}

func parseSearchCard(doc *goquery.Document, product *models.Product) string {
	var foundLink string
	doc.Find(".product-card").Each(func(i int, sel *goquery.Selection) {
		if product.Name != "" {
			return
		}

		name := sel.Find(".product-card__title a").Text()
		if name == "" {
			return
		}

		productURL, _ := sel.Find(".product-card__title a").Attr("href")
		if productURL != "" {
			if strings.HasPrefix(productURL, "http") {
				product.URL = productURL
			} else if strings.HasPrefix(productURL, "/") {
				product.URL = "https://www.apotheke.at" + productURL
			}
			foundLink = product.URL
		}

		product.Name = strings.TrimSpace(name)

		priceStr := sel.Find(".product-card__price--red [aria-hidden='true'] span:first-child").Text()
		if priceStr == "" {
			priceStr = sel.Find(".product-card__price div[aria-hidden='true'] span:first-child").Text()
		}
		if priceStr != "" {
			product.Price = common.ParsePrice(priceStr)
		}

		oldPriceStr := sel.Find(".product-card__price--cross-out").Text()
		if oldPriceStr != "" {
			if val := common.ParsePrice(oldPriceStr); val > 0 {
				product.OldPrice = val
				product.IsDiscounted = true
			}
		}

		availabilityText := sel.Find(".availability span").Text()
		if availabilityText != "" {
			product.IsAvailable, product.AvailabilityLabel = common.CheckAvailability(availabilityText)
		}

		apoPunkteText := sel.Find(".pdp-buy-box__bonus-text, .product-card__bonus-text").Text()
		if apoPunkteText == "" {
			sel.Find(".product-card__info-details div, .product-card__highlight-text li, span").EachWithBreak(func(_ int, el *goquery.Selection) bool {
				text := strings.TrimSpace(el.Text())
				if strings.Contains(strings.ToLower(text), "apopunkte") {
					apoPunkteText = text
					return false
				}
				return true
			})
		}

		if apoPunkteText != "" {
			apoPunkteText = strings.TrimSpace(apoPunkteText)
			if product.DiscountLabel != "" {
				product.DiscountLabel += " | " + apoPunkteText
			} else {
				product.DiscountLabel = apoPunkteText
			}
			product.IsDiscounted = true
		}

		unitDetails := sel.Find(".product-card__unit-details").Text()
		if unitDetails != "" {
			product.PriceDetails = strings.TrimSpace(unitDetails)
		}

		ratingStyle, _ := sel.Find(".product-card__rating-foreground").Attr("style")
		if ratingStyle != "" {
			re := regexp.MustCompile(`width:\s*([\d.]+)%`)
			matches := re.FindStringSubmatch(ratingStyle)
			if len(matches) > 1 {
				if percent, err := strconv.ParseFloat(matches[1], 64); err == nil {
					product.Rating = (percent / 100.0) * 5.0
				}
			}
		}

		reviewCountStr := sel.Find(".product-card__review-count").Text()
		if reviewCountStr != "" {
			reviewCountStr = strings.Trim(strings.TrimSpace(reviewCountStr), "()")
			if count, err := strconv.Atoi(reviewCountStr); err == nil {
				product.ReviewCount = count
			}
		}
	})
	return foundLink
}

func parsePDP(doc *goquery.Document, product *models.Product) {
	sel := doc.Find("#product-detail-wrapper")
	if sel.Length() == 0 {
		return
	}

	name := sel.Find("h1#pdp-product-title").Text()
	if name == "" {
		return
	}
	product.Name = strings.TrimSpace(name)

	priceStr := sel.Find(".product-detail-current-price").Text()
	if priceStr != "" {
		product.Price = common.ParsePrice(priceStr)
	}

	oldPriceStr := sel.Find(".product-detail-original-price").Text()
	if oldPriceStr != "" {
		if val := common.ParsePrice(oldPriceStr); val > 0 {
			product.OldPrice = val
			product.IsDiscounted = true
		}
	}

	availabilityText := sel.Find(".pdp-buy-box__status-text").Text()
	if availabilityText != "" {
		product.IsAvailable, product.AvailabilityLabel = common.CheckAvailability(availabilityText)
	}

	apoPunkteText := sel.Find(".pdp-buy-box__bonus-text").Text()
	if apoPunkteText != "" {
		apoPunkteText = strings.TrimSpace(apoPunkteText)
		if !strings.Contains(product.DiscountLabel, apoPunkteText) {
			if product.DiscountLabel != "" {
				product.DiscountLabel += " | " + apoPunkteText
			} else {
				product.DiscountLabel = apoPunkteText
			}
			product.IsDiscounted = true
		}
	}

	scoreStr := sel.Find(".pdp-reviews__score").Text()
	if scoreStr != "" {
		scoreStr = strings.ReplaceAll(scoreStr, ",", ".")
		if val, err := strconv.ParseFloat(strings.TrimSpace(scoreStr), 64); err == nil {
			product.Rating = val
		}
	}

	reviewCountStr := sel.Find(".pdp-reviews__count").Text()
	if reviewCountStr == "" {
		reviewCountStr = sel.Find(".pdp-buy-box__rating-count").Text()
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
}
