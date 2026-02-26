package cache

import (
	"database/sql"
	"encoding/json"
	"hunter-base/pkg/models"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

type Cache struct {
	db  *sql.DB
	ttl time.Duration
}

func New(dbPath string, ttl time.Duration) (*Cache, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS products (
			store TEXT NOT NULL,
			product_id TEXT NOT NULL,
			data TEXT NOT NULL,
			scraped_at DATETIME NOT NULL,
			PRIMARY KEY (store, product_id)
		)
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Cache{db: db, ttl: ttl}, nil
}

func (c *Cache) Get(store, productID string) (*models.Product, bool) {
	var data string
	var scrapedAt time.Time

	err := c.db.QueryRow(
		`SELECT data, scraped_at FROM products WHERE store = ? AND product_id = ?`,
		store, productID,
	).Scan(&data, &scrapedAt)

	if err != nil {
		return nil, false
	}

	if time.Since(scrapedAt) > c.ttl {
		return nil, false
	}

	var product models.Product
	if err := json.Unmarshal([]byte(data), &product); err != nil {
		log.Printf("Cache: failed to unmarshal product %s/%s: %v", store, productID, err)
		return nil, false
	}

	return &product, true
}

func (c *Cache) Set(store, productID string, product *models.Product) {
	data, err := json.Marshal(product)
	if err != nil {
		log.Printf("Cache: failed to marshal product %s/%s: %v", store, productID, err)
		return
	}

	_, err = c.db.Exec(
		`INSERT INTO products (store, product_id, data, scraped_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(store, product_id)
		 DO UPDATE SET data = excluded.data, scraped_at = excluded.scraped_at`,
		store, productID, string(data), product.ScrapedAt,
	)
	if err != nil {
		log.Printf("Cache: failed to store product %s/%s: %v", store, productID, err)
	}
}

func (c *Cache) Close() error {
	return c.db.Close()
}
