package billa

import (
	"fmt"
	"hunter-base/pkg/models"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

const (
	Source  = "BILLA"
	BaseURL = "https://shop.billa.at/produkte/"
)

type Scraper struct {
	Collector *colly.Collector
}

func NewScraper() *Scraper {
	c := colly.NewCollector(
		colly.AllowedDomains("shop.billa.at"),
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"),
	)
	return &Scraper{
		Collector: c,
	}
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	product := &models.Product{
		Source:    Source,
		ID:        productID,
		URL:       BaseURL + productID,
		Currency:  "EUR",
		ScrapedAt: time.Now(),
	}

	s.Collector.OnHTML("h1", func(e *colly.HTMLElement) {
		product.Name = strings.TrimSpace(e.Text)
	})
	s.Collector.OnHTML(".ws-product-detail-main__price", func(e *colly.HTMLElement) {
		priceStr := e.ChildText(".ws-product-price-type__value")
		if priceStr != "" {
			priceStr = strings.TrimSpace(priceStr)
			priceStr = strings.ReplaceAll(priceStr, "€", "")
			priceStr = strings.ReplaceAll(priceStr, ",", ".")
			priceStr = strings.TrimSpace(priceStr)

			if val, err := strconv.ParseFloat(priceStr, 64); err == nil {
				product.Price = val
				product.IsAvailable = true
			}
		}

		oldPriceStr := e.ChildText(".ws-product-price-strike")
		if oldPriceStr != "" {
			oldPriceStr = strings.TrimSpace(oldPriceStr)
			oldPriceStr = strings.ReplaceAll(oldPriceStr, "€", "")
			oldPriceStr = strings.ReplaceAll(oldPriceStr, ",", ".")
			oldPriceStr = strings.TrimSpace(oldPriceStr)

			if val, err := strconv.ParseFloat(oldPriceStr, 64); err == nil {
				product.OldPrice = val
			}
		}
	})

	log.Printf("Navigating to %s", product.URL)
	err := s.Collector.Visit(product.URL)
	if err != nil {
		return nil, err
	}

	if product.Name == "" {
		return nil, fmt.Errorf("failed to scrape product name")
	}

	return product, nil
}
