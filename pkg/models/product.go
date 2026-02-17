package models

import "time"

type Product struct {
	Source       string    `json:"source"`
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Price        float64   `json:"price"`
	OldPrice     float64   `json:"old_price,omitempty"`
	Currency     string    `json:"currency"`
	URL          string    `json:"url"`
	ScrapedAt    time.Time `json:"scraped_at"`
	IsAvailable  bool      `json:"is_available"`
	IsDiscounted bool      `json:"is_discounted"`
}
