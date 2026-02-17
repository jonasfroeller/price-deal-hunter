package spar

import (
	"context"
	"fmt"
	"hunter-base/pkg/models"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	Source  = "SPAR"
	BaseURL = "https://www.spar.at/produktwelt/p"
)

type Scraper struct{}

func NewScraper() *Scraper {
	return &Scraper{}
}

func (s *Scraper) Scrape(productID string) (*models.Product, error) {
	product := &models.Product{
		Source:    Source,
		ID:        productID,
		URL:       BaseURL + productID,
		Currency:  "EUR",
		ScrapedAt: time.Now(),
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	scrapeCtx, cancelScrape := context.WithTimeout(ctx, 60*time.Second)
	defer cancelScrape()

	var name, priceStr, oldPriceStr, articleNumber string

	log.Printf("Navigating to %s", product.URL)

	err := chromedp.Run(scrapeCtx,
		chromedp.Navigate(product.URL),
		chromedp.WaitReady(`h1.heading__title, h1[data-tosca='pdp-heading']`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("h1[data-tosca='pdp-heading']")?.innerText || document.querySelector("h1.heading__title")?.innerText || ""`, &name),
		chromedp.Evaluate(`document.querySelector(".product-price__price")?.innerText || ""`, &priceStr),
		chromedp.Evaluate(`document.querySelector(".product-price__price-old")?.innerText || ""`, &oldPriceStr),
		chromedp.Evaluate(`
			(function() {
				const el = document.querySelector(".pdp__meta-entry[data-tosca='pdp-article-number']");
				return el ? el.innerText : "";
			})()
		`, &articleNumber),
	)

	if err != nil {
		log.Printf("Chromedp run failed: %v", err)
		debugCtx, cancelDebug := context.WithTimeout(ctx, 30*time.Second)
		defer cancelDebug()

		var buf []byte
		if errShot := chromedp.Run(debugCtx, chromedp.CaptureScreenshot(&buf)); errShot != nil {
			log.Printf("Failed to capture screenshot: %v", errShot)
		} else {
			if errWrite := os.WriteFile("spar_debug.png", buf, 0644); errWrite != nil {
				log.Printf("Failed to write screenshot: %v", errWrite)
			} else {
				log.Println("Screenshot saved to spar_debug.png")
			}
		}

		var html string
		if errHTML := chromedp.Run(debugCtx, chromedp.Evaluate(`document.documentElement.outerHTML`, &html)); errHTML != nil {
			log.Printf("Failed to capture HTML: %v", errHTML)
		} else {
			if errWrite := os.WriteFile("spar_debug.html", []byte(html), 0644); errWrite != nil {
				log.Printf("Failed to write HTML: %v", errWrite)
			} else {
				log.Println("HTML saved to spar_debug.html")
			}
		}

		return nil, fmt.Errorf("chromedp failed: %w", err)
	}

	product.Name = strings.TrimSpace(name)
	product.Name = strings.ReplaceAll(product.Name, "\n", " ")

	if priceStr != "" {
		priceStr = strings.TrimSpace(priceStr)
		priceStr = strings.ReplaceAll(priceStr, ",", ".")
		if val, err := strconv.ParseFloat(priceStr, 64); err == nil {
			product.Price = val
			product.IsAvailable = true
		}
	}

	if oldPriceStr != "" {
		oldPriceStr = strings.TrimSpace(oldPriceStr)
		oldPriceStr = strings.TrimPrefix(oldPriceStr, "statt ")
		oldPriceStr = strings.ReplaceAll(oldPriceStr, ",", ".")
		if val, err := strconv.ParseFloat(oldPriceStr, 64); err == nil {
			product.OldPrice = val
			product.IsDiscounted = true
		}
	}

	if articleNumber != "" {
		parts := strings.Split(articleNumber, ":")
		if len(parts) > 1 {
			id := strings.TrimSpace(parts[1])
			if id != productID {
				fmt.Printf("Warning: Scraped ID %s does not match requested ID %s\n", id, productID)
			}
		}
	}

	if product.Name == "" {
		return nil, models.ErrProductNotFound
	}

	return product, nil
}
