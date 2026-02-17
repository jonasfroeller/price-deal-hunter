package lidl

import (
	"encoding/json"
	"fmt"
	"hunter-base/pkg/models"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

const (
	Source  = "LIDL"
	BaseURL = "https://www.lidl.at/p/"
)

type Scraper struct {
	Collector *colly.Collector
	BaseURL   string
}

func NewScraper() *Scraper {
	c := colly.NewCollector(
		colly.AllowedDomains("www.lidl.at", "127.0.0.1"), // localhost for testing
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"),
	)
	return &Scraper{
		Collector: c,
		BaseURL:   "https://www.lidl.at/p/product/p",
	}
}

type lidlDataLayer struct {
	Id       string  `json:"id"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	Brand    string  `json:"brand"`
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	url := fmt.Sprintf("%s%s", s.BaseURL, productID)

	product := &models.Product{
		Source:    Source,
		ID:        productID,
		URL:       url,
		Currency:  "EUR",
		ScrapedAt: time.Now(),
	}

	var jsonFound bool

	s.Collector.OnHTML("script", func(e *colly.HTMLElement) {
		if jsonFound {
			return
		}
		text := strings.TrimSpace(e.Text)
		if strings.Contains(text, "unified_datalayer_product") {
			loc := strings.Index(text, "unified_datalayer_product")
			if loc != -1 {
				equalsPos := strings.Index(text[loc:], "=")
				if equalsPos != -1 {
					startFunc := loc + equalsPos + 1
					bracePos := strings.Index(text[startFunc:], "{")
					if bracePos != -1 {
						jsonStart := startFunc + bracePos
						jsonStr := text[jsonStart:]
						jsonStr = strings.TrimRight(jsonStr, ";")

						var data lidlDataLayer
						if err := json.Unmarshal([]byte(jsonStr), &data); err == nil {
							product.Name = data.Name
							product.Price = data.Price
							product.Currency = data.Currency
							jsonFound = true
							product.IsAvailable = true
						}
					}
				}
			}
		}
	})

	// Availability & Labels
	// Keywords: "Mengenrabatt", "AKTION", "Billiger", "Filiale"

	s.Collector.OnHTML("body", func(e *colly.HTMLElement) {
		fullText := e.Text

		if strings.Contains(fullText, "Mengenrabatt") {
			product.DiscountLabel = "Mengenrabatt"
			product.IsDiscounted = true
		} else if strings.Contains(fullText, "AKTION") {
			product.DiscountLabel = "AKTION"
			product.IsDiscounted = true
		} else if strings.Contains(fullText, "Billiger") {
			product.DiscountLabel = "Billiger"
			product.IsDiscounted = true
		}

		if strings.Contains(fullText, "Lidl Plus") {
			if product.DiscountLabel != "" {
				product.DiscountLabel += " + Lidl Plus"
			} else {
				product.DiscountLabel = "Lidl Plus"
			}
			product.IsDiscounted = true
		}

		// Availability dates
		// Pattern: "in der Filiale" followed by date range
		if strings.Contains(fullText, "Filiale") {
			reDate := regexp.MustCompile(`(\d{2}\.\d{2}\.\s*-\s*\d{2}\.\d{2}\.)`)
			dateMatch := reDate.FindString(fullText)
			if dateMatch != "" {
				product.AvailabilityLabel = "Filiale " + dateMatch
			} else {
				reSingleDate := regexp.MustCompile(`(ab\s*\d{2}\.\d{2}\.)`)
				singleMatch := reSingleDate.FindString(fullText)
				if singleMatch != "" {
					product.AvailabilityLabel = "Filiale " + singleMatch
				} else {
					product.AvailabilityLabel = "In der Filiale"
				}
			}
		}
	})

	// Check for old price (Strikethrough)
	s.Collector.OnHTML(".ods-price__stroke-price", func(e *colly.HTMLElement) {
		oldPriceText := e.Text
		// Clean up currency
		cleaned := strings.ReplaceAll(oldPriceText, "€", "")
		cleaned = strings.TrimSpace(cleaned)
		// If formatting is german "4,99"
		cleaned = strings.ReplaceAll(cleaned, ",", ".")

		// Extract regex float
		reFloat := regexp.MustCompile(`\d+\.\d+`)
		floatMatch := reFloat.FindString(cleaned)
		if floatMatch != "" {
			fmt.Sscanf(floatMatch, "%f", &product.OldPrice)
			product.IsDiscounted = true
		}
	})

	log.Printf("Navigating to %s", product.URL)
	err := s.Collector.Visit(product.URL)
	if err != nil {
		return nil, err
	}

	if product.Name == "" {
		return nil, models.ErrProductNotFound
	}

	return product, nil
}
