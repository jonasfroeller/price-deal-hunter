package models

import "time"

type Variant struct {
	Name          string  `json:"name"`
	Price         float64 `json:"price"`
	OldPrice      float64 `json:"old_price,omitempty"`
	IsDiscounted  bool    `json:"is_discounted"`
	DiscountLabel string  `json:"discount_label,omitempty"`
	PriceDetails  string  `json:"price_details,omitempty"`
	URL           string  `json:"url,omitempty"`
}

type Product struct {
	Source            string    `json:"source"`
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Price             float64   `json:"price"`
	OldPrice          float64   `json:"old_price,omitempty"`
	Currency          string    `json:"currency"`
	URL               string    `json:"url"`
	ScrapedAt         time.Time `json:"scraped_at"`
	IsAvailable       bool      `json:"is_available"`
	IsDiscounted      bool      `json:"is_discounted"`
	DiscountLabel     string    `json:"discount_label,omitempty"`
	AvailabilityLabel string    `json:"availability_label,omitempty"`
	PriceDetails      string    `json:"price_details,omitempty"`
	Rating            float64   `json:"rating,omitempty"`
	ReviewCount       int       `json:"review_count,omitempty"`
	Variants          []Variant `json:"variants,omitempty"`
}
