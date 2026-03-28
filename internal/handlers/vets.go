package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/places"
)

type District struct {
	Lat    float64
	Lng    float64
	Radius float64
}

var hkDistricts = map[string]District{
	"central_and_western": {Lat: 22.28666, Lng: 114.15497, Radius: 2000},
	"eastern":             {Lat: 22.28411, Lng: 114.22414, Radius: 2000},
	"southern":            {Lat: 22.24725, Lng: 114.15884, Radius: 2000},
	"wan_chai":            {Lat: 22.27968, Lng: 114.17168, Radius: 2000},
	"kowloon_city":        {Lat: 22.32820, Lng: 114.19155, Radius: 2000},
	"kwun_tong":           {Lat: 22.31326, Lng: 114.22581, Radius: 2000},
	"sham_shui_po":        {Lat: 22.33074, Lng: 114.16220, Radius: 2000},
	"wong_tai_sin":        {Lat: 22.33353, Lng: 114.19686, Radius: 2000},
	"yau_tsim_mong":       {Lat: 22.32138, Lng: 114.17260, Radius: 2000},
	"islands":             {Lat: 22.26114, Lng: 113.94608, Radius: 2000},
	"kwai_tsing":          {Lat: 22.35488, Lng: 114.08401, Radius: 2000},
	"north":               {Lat: 22.49471, Lng: 114.13812, Radius: 2000},
	"sai_kung":            {Lat: 22.38143, Lng: 114.27052, Radius: 2000},
	"sha_tin":             {Lat: 22.38715, Lng: 114.19534, Radius: 2000},
	"tai_po":              {Lat: 22.45085, Lng: 114.16422, Radius: 2000},
	"tsuen_wan":           {Lat: 22.36281, Lng: 114.12907, Radius: 2000},
	"tuen_mun":            {Lat: 22.39163, Lng: 113.97708, Radius: 2000},
	"yuen_long":           {Lat: 22.44559, Lng: 114.02218, Radius: 2000},
}

func NewVetsHandler(client *places.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check for text search query first
		queryParam := r.URL.Query().Get("q")
		var placesList []places.Place
		var err error

		if queryParam != "" {
			// Perform text search
			placesList, err = client.SearchTextVets(queryParam)
		} else {
			// Perform district search (existing logic)
			districtParam := r.URL.Query().Get("district")
			if districtParam == "" {
				http.Error(w, "district query parameter or 'q' search parameter is required", http.StatusBadRequest)
				return
			}

			// Normalize district input (lowercase, snake_case)
			districtKey := strings.ToLower(districtParam)
			district, ok := hkDistricts[districtKey]
			if !ok {
				http.Error(w, fmt.Sprintf("unknown district: %s", districtParam), http.StatusBadRequest)
				return
			}

			placesList, err = client.SearchNearbyVets(district.Lat, district.Lng, district.Radius)
		}

		openNowParam := r.URL.Query().Get("open_now")
		filterOpenNow := openNowParam == "true"

		if err != nil {
			// Log error internally if logging capability existed, but for now just error to client
			// Maybe 502 Bad Gateway if upstream failed
			if strings.Contains(err.Error(), "google places api returned status") {
				http.Error(w, "Failed to fetch from Google Places API", http.StatusBadGateway)
			} else {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
			fmt.Printf("Error fetching vets: %v\n", err)
			return
		}

		var filteredPlaces []places.Place
		if filterOpenNow {
			for _, p := range placesList {
				if p.OpenNow != nil && *p.OpenNow {
					filteredPlaces = append(filteredPlaces, p)
				}
			}
		} else {
			filteredPlaces = placesList
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(filteredPlaces); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
	}
}
