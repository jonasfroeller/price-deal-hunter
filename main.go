package main

import (
	"encoding/json"
	"fmt"
	"hunter-base/pkg/models"
	"hunter-base/pkg/scrapers/billa"
	"hunter-base/pkg/scrapers/spar"
	"log"
	"net"
	"net/http"
	"strings"
)

func main() {
	http.HandleFunc("/stores/", productHandler)

	port := "9090"
	fmt.Printf("Starting server on :%s...\n", port)

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
		http.Error(w, "Invalid path. Expected /stores/{store}/products/{id}", http.StatusBadRequest)
		return
	}

	store := strings.ToLower(parts[2])
	productID := parts[4]

	var product *models.Product
	var err error

	switch store {
	case "spar":
		scraper := spar.NewScraper()
		product, err = scraper.Scrape(productID)
	case "billa":
		scraper := billa.NewScraper()
		product, err = scraper.Scrape(productID)
	default:
		http.Error(w, "Store not supported. Available: spar, billa", http.StatusBadRequest)
		return
	}

	if err != nil {
		log.Printf("Error scraping %s %s: %v", store, productID, err)
		http.Error(w, fmt.Sprintf("Failed to get product: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(product); err != nil {
		log.Printf("Error encoding response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}
