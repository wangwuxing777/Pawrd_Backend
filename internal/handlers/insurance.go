package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

const insuranceDBPath = "assets/pet_insurance.db"

func OpenInsuranceDB() (*sql.DB, error) {
	if _, err := os.Stat(insuranceDBPath); err == nil {
		return sql.Open("sqlite3", insuranceDBPath)
	}

	ex, err := os.Executable()
	if err == nil {
		path := filepath.Join(filepath.Dir(ex), insuranceDBPath)
		if _, err := os.Stat(path); err == nil {
			return sql.Open("sqlite3", path)
		}
	}

	return sql.Open("sqlite3", insuranceDBPath)
}

func InsuranceCompaniesHandler(w http.ResponseWriter, r *http.Request) {
	EnableCors(&w)
	if r.Method == http.MethodOptions {
		return
	}

	db, err := OpenInsuranceDB()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.Query("SELECT company_id, company_name, company_name_zh, company_logo FROM insurance_provider")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var companies []models.InsuranceCompany
	for rows.Next() {
		var c models.InsuranceCompany
		if err := rows.Scan(&c.CompanyId, &c.CompanyName, &c.CompanyNameZh, &c.CompanyLogo); err == nil {
			companies = append(companies, c)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(companies)
}

func InsuranceProductsHandler(w http.ResponseWriter, r *http.Request) {
	EnableCors(&w)
	if r.Method == http.MethodOptions {
		return
	}

	db, err := OpenInsuranceDB()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.Query(`SELECT insurance_id, provider_id, insurance_name, insurance_name_zh, remark, remark_zh, 
		min_age, min_age_zh, max_age, max_age_zh, coinsurance, coinsurance_zh, suitable_pet_type, suitable_pet_type_zh,
		cat_breed_type, cat_breed_type_zh, dog_breed_type, dog_breed_type_zh, breed_type_remark, breed_type_remark_zh,
		payment_mode, payment_mode_zh, waiting_period, waiting_period_zh, information_link, information_link_zh, update_time,
		NULL as tag, NULL as tag_zh
		FROM product`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var products []models.InsuranceProduct
	for rows.Next() {
		var p models.InsuranceProduct
		if err := rows.Scan(&p.InsuranceId, &p.ProviderId, &p.InsuranceName, &p.InsuranceNameZh, &p.Remark, &p.RemarkZh,
			&p.MinAge, &p.MinAgeZh, &p.MaxAge, &p.MaxAgeZh, &p.Coinsurance, &p.CoinsuranceZh, &p.SuitablePetType, &p.SuitablePetTypeZh,
			&p.CatBreedType, &p.CatBreedTypeZh, &p.DogBreedType, &p.DogBreedTypeZh, &p.BreedTypeRemark, &p.BreedTypeRemarkZh,
			&p.PaymentMode, &p.PaymentModeZh, &p.WaitingPeriod, &p.WaitingPeriodZh, &p.InformationLink, &p.InformationLinkZh, &p.UpdateTime,
			&p.Tag, &p.TagZh); err == nil {
			products = append(products, p)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(products)
}

func CoverageListHandler(w http.ResponseWriter, r *http.Request) {
	EnableCors(&w)
	if r.Method == http.MethodOptions {
		return
	}

	db, err := OpenInsuranceDB()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.Query("SELECT coverage_id, coverage_type, coverage_type_zh FROM coverage_list")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []models.CoverageItem
	for rows.Next() {
		var item models.CoverageItem
		if err := rows.Scan(&item.CoverageId, &item.CoverageType, &item.CoverageTypeZh); err == nil {
			list = append(list, item)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func CoverageLimitsHandler(w http.ResponseWriter, r *http.Request) {
	EnableCors(&w)
	if r.Method == http.MethodOptions {
		return
	}

	db, err := OpenInsuranceDB()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.Query("SELECT coverage_id, product_id, coverage_limit, remark, remark_zh FROM coverage_limit")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var limits []models.CoverageLimit
	for rows.Next() {
		var l models.CoverageLimit
		if err := rows.Scan(&l.CoverageId, &l.ProductId, &l.CoverageLimit, &l.Remark, &l.RemarkZh); err == nil {
			limits = append(limits, l)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(limits)
}

func SubCoverageLimitsHandler(w http.ResponseWriter, r *http.Request) {
	EnableCors(&w)
	if r.Method == http.MethodOptions {
		return
	}

	db, err := OpenInsuranceDB()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	rows, err := db.Query(`SELECT sub_coverage_id, parent_coverage_id, product_id, sub_coverage_name, sub_coverage_name_zh, 
		sub_limit, sub_coverage_remark, sub_coverage_remark_zh FROM sub_coverage_limit`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var limits []models.SubCoverageLimit
	for rows.Next() {
		var l models.SubCoverageLimit
		if err := rows.Scan(&l.SubCoverageId, &l.ParentCoverageId, &l.ProductId, &l.SubCoverageName, &l.SubCoverageNameZh,
			&l.SubLimit, &l.SubCoverageRemark, &l.SubCoverageRemarkZh); err == nil {
			limits = append(limits, l)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(limits)
}

// Legacy Handlers - Return empty
func InsuranceProvidersHandler(w http.ResponseWriter, r *http.Request) {
	EnableCors(&w)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("[]"))
}

func ServiceSubcategoriesHandler(w http.ResponseWriter, r *http.Request) {
	EnableCors(&w)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("[]"))
}
