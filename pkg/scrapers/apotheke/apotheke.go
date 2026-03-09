package apotheke

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

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	product := &models.Product{
		Source:    Source,
		ID:        productID,
		URL:       s.BaseURL + productID,
		Currency:  "EUR",
		ScrapedAt: time.Now(),
	}

	searchURL := product.URL

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

	fetchPage := func(targetURL string) (*goquery.Document, string, error) {
		log.Printf("Navigating to %s", targetURL)
		var html, finalURL string
		err := chromedp.Run(ctx,
			chromedp.Navigate(targetURL),
			chromedp.ActionFunc(func(execCtx context.Context) error {
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
						var hasResults bool
						if err := chromedp.Evaluate(`!!(document.querySelector(".search-result-header") || document.querySelector("#product-detail-wrapper") || document.querySelector(".product-card-list"))`, &hasResults).Do(execCtx); err == nil && hasResults {
							return nil
						}
					}
				}
			}),
			chromedp.Sleep(2*time.Second),
			chromedp.Evaluate(`window.location.href`, &finalURL),
			chromedp.OuterHTML(`html`, &html, chromedp.ByQuery),
		)
		if err != nil {
			return nil, "", err
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse HTML: %w", err)
		}
		return doc, finalURL, nil
	}

	parseSearchCard := func(doc *goquery.Document) string {
		var foundLink string
		doc.Find(".product-card-list .product-card").Each(func(i int, sel *goquery.Selection) {
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

			// Regular Price
			priceStr := sel.Find(".product-card__price--red [aria-hidden='true'] span:first-child").Text()
			if priceStr == "" {
				priceStr = sel.Find(".product-card__price div[aria-hidden='true'] span:first-child").Text()
			}
			if priceStr != "" {
				priceStr = strings.TrimSpace(priceStr)
				priceStr = strings.ReplaceAll(priceStr, ",", ".")
				if val, err := strconv.ParseFloat(priceStr, 64); err == nil {
					product.Price = val
				}
			}

			// Old/Strikethrough Price
			oldPriceStr := sel.Find(".product-card__price--cross-out").Text()
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
			availabilityText := sel.Find(".availability span").Text()
			if availabilityText != "" {
				availLower := strings.ToLower(strings.TrimSpace(availabilityText))
				product.AvailabilityLabel = strings.TrimSpace(availabilityText)

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

			// apoPunkte / Discount Label extensions
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

			// Price Details (Unit price)
			unitDetails := sel.Find(".product-card__unit-details").Text()
			if unitDetails != "" {
				product.PriceDetails = strings.TrimSpace(unitDetails)
			}

			// Rating
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

			// Review Count
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

	parsePDP := func(doc *goquery.Document) {
		sel := doc.Find("#product-detail-wrapper")
		if sel.Length() == 0 {
			return
		}

		name := sel.Find("h1#pdp-product-title").Text()
		if name == "" {
			return
		}
		product.Name = strings.TrimSpace(name)

		// Current Price
		priceStr := sel.Find(".product-detail-current-price").Text()
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
		oldPriceStr := sel.Find(".product-detail-original-price").Text()
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
		availabilityText := sel.Find(".pdp-buy-box__status-text").Text()
		if availabilityText != "" {
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
		apoPunkteText := sel.Find(".pdp-buy-box__bonus-text").Text()
		if apoPunkteText != "" {
			apoPunkteText = strings.TrimSpace(apoPunkteText)
			// Avoid double-adding if we already hit it via search list
			if !strings.Contains(product.DiscountLabel, apoPunkteText) {
				if product.DiscountLabel != "" {
					product.DiscountLabel += " | " + apoPunkteText
				} else {
					product.DiscountLabel = apoPunkteText
				}
				product.IsDiscounted = true
			}
		}

		// Rating & Reviews
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

	searchDoc, _, err := fetchPage(searchURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch search page: %w", err)
	}

	// Try parsing it as a search result first
	foundLink := parseSearchCard(searchDoc)

	// If parsing search results yielded a product link different from our initial searchURL,
	// fetch that specific product page to gather all possible details
	if product.Name != "" && foundLink != "" && foundLink != searchURL {
		pdpDoc, _, pdpErr := fetchPage(foundLink)
		if pdpErr == nil {
			parsePDP(pdpDoc)
		} else {
			log.Printf("Failed to fetch PDP %s: %v", foundLink, pdpErr)
		}
	} else {
		// If it's not a list, maybe it redirected directly to a PDP
		if product.Name == "" {
			parsePDP(searchDoc)
		}
	}

	if product.Name == "" {
		return nil, models.ErrProductNotFound
	}

	return product, nil
}
