package billa

import (
	"hunter-base/pkg/models"
	"hunter-base/pkg/scrapers/common"
	"log"
	"net/http"
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
	c.WithTransport(&http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	})
	c.SetRequestTimeout(30 * time.Second)
	return &Scraper{
		Collector: c,
	}
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	product := common.NewProduct(Source, productID, BaseURL+productID)

	s.Collector.OnHTML("h1", func(e *colly.HTMLElement) {
		product.Name = strings.TrimSpace(e.Text)
	})
	s.Collector.OnHTML(".ws-product-detail-main__price", func(e *colly.HTMLElement) {
		priceStr := e.ChildText(".ws-product-price-type__value")
		if priceStr != "" {
			if val := common.ParsePrice(priceStr); val > 0 {
				product.Price = val
				product.IsAvailable = true
			}
		}

		oldPriceStr := e.ChildText(".ws-product-price-strike")
		if oldPriceStr != "" {
			if val := common.ParsePrice(oldPriceStr); val > 0 {
				product.OldPrice = val
				product.IsDiscounted = true
			}
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
