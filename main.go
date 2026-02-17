package main

import (
	"encoding/json"
	"fmt"
	"hunter-base/pkg/api"
	"hunter-base/pkg/models"
	"hunter-base/pkg/scrapers/billa"
	"hunter-base/pkg/scrapers/lidl"
	"hunter-base/pkg/scrapers/spar"
	"log"
	"net"
	"net/http"
	"strings"
)

func main() {
	http.HandleFunc("/stores/", productHandler)

	port := "9090"

	ip := GetOutboundIP()
	if ip != nil {
		fmt.Printf("Local Network URL: http://%s:%s\n", ip.String(), port)
	} else {
		fmt.Println("Could not determine local IP address.")
	}
	fmt.Printf("Access URL: http://localhost:%s\n", port)

	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		addrs, _ := net.InterfaceAddrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP
				}
			}
		}
		return nil
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

func productHandler(w http.ResponseWriter, r *http.Request) {
	// Path expected: /stores/{store}/products/{id}
	parts := strings.Split(r.URL.Path, "/")
	// parts[0] = ""
	// parts[1] = "stores"
	// parts[2] = {store}
	// parts[3] = "products"
	// parts[4] = {id}

	if len(parts) < 5 || parts[3] != "products" {
		api.WriteBadRequest(w, "Invalid path. Expected /stores/{store}/products/{id}", r.URL.Path)
		return
	}

	store := strings.ToLower(parts[2])
	rawID := parts[4]

	// Filter out non-numeric characters from the ID
	// e.g. "00-626061" -> "00626061"
	productID := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, rawID)

	if productID == "" {
		api.WriteBadRequest(w, fmt.Sprintf("Invalid product ID: %s. Must contain at least one digit.", rawID), r.URL.Path)
		return
	}

	var product *models.Product
	var err error

	switch store {
	case "spar":
		scraper := spar.NewScraper()
		product, err = scraper.Scrape(productID)
	case "billa":
		scraper := billa.NewScraper()
		product, err = scraper.Scrape(productID)
	case "lidl":
		scraper := lidl.NewScraper()
		product, err = scraper.Scrape(productID)
	default:
		api.WriteBadRequest(w, "Store not supported. Available: spar, billa, lidl", r.URL.Path)
		return
	}

	if err != nil {
		log.Printf("Error scraping %s %s: %v", store, productID, err)

		if strings.Contains(err.Error(), "context deadline exceeded") {
			api.WriteError(w, http.StatusGatewayTimeout, "Gateway Timeout", "Upstream service timed out", r.URL.Path)
			return
		}

		api.WriteInternalServerError(w, err, r.URL.Path)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(product); err != nil {
		log.Printf("Error encoding response: %v", err)
		api.WriteInternalServerError(w, fmt.Errorf("failed to encode response"), r.URL.Path)
	}
}
