package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/gorm"
)

type privatePetVaccinationDTO struct {
	ID   string    `json:"id,omitempty"`
	Name string    `json:"name"`
	Date time.Time `json:"date"`
}

type privatePetMedicalVisitDTO struct {
	ID        string    `json:"id,omitempty"`
	Date      time.Time `json:"date"`
	Clinic    string    `json:"clinic,omitempty"`
	Vet       string    `json:"vet,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Diagnosis string    `json:"diagnosis,omitempty"`
	Treatment string    `json:"treatment,omitempty"`
	Cost      float64   `json:"cost"`
	Notes     string    `json:"notes,omitempty"`
}

type privatePetMedicationDTO struct {
	ID        string     `json:"id,omitempty"`
	Name      string     `json:"name"`
	Dosage    string     `json:"dosage,omitempty"`
	Frequency string     `json:"frequency,omitempty"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
	Notes     string     `json:"notes,omitempty"`
}

type privatePetWeightEntryDTO struct {
	ID       string    `json:"id,omitempty"`
	Date     time.Time `json:"date"`
	WeightKg float64   `json:"weight_kg"`
	Notes    string    `json:"notes,omitempty"`
}

type privatePetDTO struct {
	ID             string                        `json:"id"`
	FamilyPetID    string                        `json:"family_pet_id,omitempty"`
	PublicSlug     string                        `json:"public_slug,omitempty"`
	Name           string                        `json:"name"`
	Species        string                        `json:"species"`
	Breed          string                        `json:"breed,omitempty"`
	Sex            string                        `json:"sex,omitempty"`
	BirthYear      int                           `json:"birth_year"`
	AvatarURL      string                        `json:"avatar_url,omitempty"`
	WeightKg       *float64                      `json:"weight_kg,omitempty"`
	IsNeutered     bool                          `json:"is_neutered"`
	MicrochipID    string                        `json:"microchip_id,omitempty"`
	Allergies      string                        `json:"allergies,omitempty"`
	Notes          string                        `json:"notes,omitempty"`
	Vaccinations   []privatePetVaccinationDTO   `json:"vaccinations,omitempty"`
	MedicalVisits  []privatePetMedicalVisitDTO  `json:"medical_visits,omitempty"`
	Medications    []privatePetMedicationDTO    `json:"medications,omitempty"`
	WeightEntries  []privatePetWeightEntryDTO   `json:"weight_entries,omitempty"`
}

func NewPrivatePetsHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPut {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}

		switch r.Method {
		case http.MethodGet:
			payload, status, err := loadPrivatePets(db, userID)
			if err != nil {
				http.Error(w, "failed to load private pets: "+err.Error(), status)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"pets": payload})

		case http.MethodPut:
			var req struct {
				Pets []privatePetDTO `json:"pets"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAuthError(w, http.StatusBadRequest, "Invalid request body")
				return
			}
			if err := replacePrivatePets(db, userID, req.Pets); err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			payload, status, err := loadPrivatePets(db, userID)
			if err != nil {
				http.Error(w, "failed to reload private pets: "+err.Error(), status)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"pets": payload})
		}
	}
}

func loadPrivatePets(db *gorm.DB, userID string) ([]privatePetDTO, int, error) {
	var family models.Family
	if err := db.Where("owner_user_id = ?", userID).First(&family).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []privatePetDTO{}, http.StatusOK, nil
		}
		return nil, http.StatusInternalServerError, err
	}

	var pets []models.Pet
	if err := db.Preload("Vaccinations").
		Preload("MedicalVisits").
		Preload("Medications").
		Preload("WeightEntries").
		Preload("PublicProfile").
		Where("family_id = ?", family.ID).
		Order("created_at ASC").
		Find(&pets).Error; err != nil {
		return nil, http.StatusInternalServerError, err
	}

	payload := make([]privatePetDTO, 0, len(pets))
	for _, pet := range pets {
		payload = append(payload, privatePetDTOOf(pet))
	}
	return payload, http.StatusOK, nil
}

func replacePrivatePets(db *gorm.DB, userID string, pets []privatePetDTO) error {
	for _, dto := range pets {
		dto.ID = strings.TrimSpace(dto.ID)
		dto.Name = strings.TrimSpace(dto.Name)
		dto.Species = strings.TrimSpace(dto.Species)
		if dto.ID == "" || dto.Name == "" || dto.Species == "" {
			return errors.New("pet id, name and species are required")
		}
		if dto.BirthYear < minPetBirthYear || dto.BirthYear > time.Now().UTC().Year() {
			return errors.New("birth_year is out of range")
		}
	}

	var user models.AuthUser
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		return errors.New("account not found")
	}
	family, err := ensureFamilyForPrivatePets(db, &user)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existing []models.Pet
		if err := tx.Preload("Vaccinations").
			Preload("MedicalVisits").
			Preload("Medications").
			Preload("WeightEntries").
			Where("family_id = ?", family.ID).
			Find(&existing).Error; err != nil {
			return err
		}
		existingByClientID := map[string]models.Pet{}
		for _, pet := range existing {
			if pet.ClientPetID != nil {
				existingByClientID[*pet.ClientPetID] = pet
			}
		}
		keepIDs := map[string]bool{}
		for _, dto := range pets {
			pet, err := upsertPrivatePet(tx, family, existingByClientID[dto.ID], dto)
			if err != nil {
				return err
			}
			keepIDs[pet.ID] = true
		}
		for _, pet := range existing {
			if !keepIDs[pet.ID] {
				if err := tx.Delete(&pet).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func ensureFamilyForPrivatePets(db *gorm.DB, user *models.AuthUser) (models.Family, error) {
	var family models.Family
	err := db.Where("owner_user_id = ?", user.ID).First(&family).Error
	if err == nil {
		return family, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return family, err
	}
	if err := createFamilyForUser(db, user); err != nil {
		return family, err
	}
	if err := db.Where("owner_user_id = ?", user.ID).First(&family).Error; err != nil {
		return family, err
	}
	return family, nil
}

func upsertPrivatePet(
	tx *gorm.DB,
	family models.Family,
	existing models.Pet,
	dto privatePetDTO,
) (models.Pet, error) {
	birthDate := birthDateFromYear(dto.BirthYear)
	pet := existing
	if pet.ID == "" {
		pet = models.Pet{
			FamilyID:    family.ID,
			ClientPetID: &dto.ID,
			Name:        dto.Name,
			Species:     dto.Species,
		}
	}
	pet.Name = dto.Name
	pet.Species = dto.Species
	pet.Breed = dto.Breed
	pet.Sex = dto.Sex
	pet.BirthDate = &birthDate
	pet.AvatarURL = dto.AvatarURL
	pet.MicrochipID = dto.MicrochipID
	pet.Allergies = dto.Allergies
	pet.PrivateNotes = dto.Notes
	pet.IsNeutered = dto.IsNeutered
	pet.CurrentWeightKg = dto.WeightKg
	if pet.ID == "" {
		if err := tx.Create(&pet).Error; err != nil {
			return pet, err
		}
	} else if err := tx.Save(&pet).Error; err != nil {
		return pet, err
	}
	for _, item := range pet.Vaccinations {
		if err := tx.Delete(&item).Error; err != nil {
			return pet, err
		}
	}
	for _, item := range pet.MedicalVisits {
		if err := tx.Delete(&item).Error; err != nil {
			return pet, err
		}
	}
	for _, item := range pet.Medications {
		if err := tx.Delete(&item).Error; err != nil {
			return pet, err
		}
	}
	for _, item := range pet.WeightEntries {
		if err := tx.Delete(&item).Error; err != nil {
			return pet, err
		}
	}
	if err := createPrivatePetRecords(tx, pet.ID, dto); err != nil {
		return pet, err
	}
	return pet, nil
}

func createPrivatePetRecords(tx *gorm.DB, petID string, dto privatePetDTO) error {
	for _, item := range dto.Vaccinations {
		if err := tx.Create(&models.PetVaccination{
			PetID: petID, Name: strings.TrimSpace(item.Name), Date: item.Date,
		}).Error; err != nil {
			return err
		}
	}
	for _, item := range dto.MedicalVisits {
		if err := tx.Create(&models.PetMedicalVisit{
			PetID: petID, Date: item.Date, Clinic: item.Clinic, Vet: item.Vet,
			Reason: item.Reason, Diagnosis: item.Diagnosis, Treatment: item.Treatment,
			Cost: item.Cost, Notes: item.Notes,
		}).Error; err != nil {
			return err
		}
	}
	for _, item := range dto.Medications {
		if err := tx.Create(&models.PetMedication{
			PetID: petID, Name: item.Name, Dosage: item.Dosage, Frequency: item.Frequency,
			StartDate: item.StartDate, EndDate: item.EndDate, Notes: item.Notes,
		}).Error; err != nil {
			return err
		}
	}
	for _, item := range dto.WeightEntries {
		if err := tx.Create(&models.PetWeightEntry{
			PetID: petID, Date: item.Date, WeightKg: item.WeightKg, Notes: item.Notes,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func privatePetDTOOf(pet models.Pet) privatePetDTO {
	slug := ""
	if pet.PublicProfile.PetID != "" {
		slug = pet.PublicProfile.Slug
	}
	clientPetID := ""
	if pet.ClientPetID != nil {
		clientPetID = *pet.ClientPetID
	}
	dto := privatePetDTO{
		ID:            clientPetID,
		FamilyPetID:   pet.ID,
		PublicSlug:    slug,
		Name:          pet.Name,
		Species:       pet.Species,
		Breed:         pet.Breed,
		Sex:           pet.Sex,
		AvatarURL:     pet.AvatarURL,
		WeightKg:      pet.CurrentWeightKg,
		IsNeutered:    pet.IsNeutered,
		MicrochipID:   pet.MicrochipID,
		Allergies:     pet.Allergies,
		Notes:         pet.PrivateNotes,
		Vaccinations:  make([]privatePetVaccinationDTO, 0, len(pet.Vaccinations)),
		MedicalVisits: make([]privatePetMedicalVisitDTO, 0, len(pet.MedicalVisits)),
		Medications:   make([]privatePetMedicationDTO, 0, len(pet.Medications)),
		WeightEntries: make([]privatePetWeightEntryDTO, 0, len(pet.WeightEntries)),
	}
	if pet.BirthDate != nil {
		dto.BirthYear = pet.BirthDate.Year()
	}
	for _, item := range pet.Vaccinations {
		dto.Vaccinations = append(dto.Vaccinations, privatePetVaccinationDTO{
			ID: item.ID, Name: item.Name, Date: item.Date,
		})
	}
	for _, item := range pet.MedicalVisits {
		dto.MedicalVisits = append(dto.MedicalVisits, privatePetMedicalVisitDTO{
			ID: item.ID, Date: item.Date, Clinic: item.Clinic, Vet: item.Vet,
			Reason: item.Reason, Diagnosis: item.Diagnosis, Treatment: item.Treatment,
			Cost: item.Cost, Notes: item.Notes,
		})
	}
	for _, item := range pet.Medications {
		dto.Medications = append(dto.Medications, privatePetMedicationDTO{
			ID: item.ID, Name: item.Name, Dosage: item.Dosage, Frequency: item.Frequency,
			StartDate: item.StartDate, EndDate: item.EndDate, Notes: item.Notes,
		})
	}
	for _, item := range pet.WeightEntries {
		dto.WeightEntries = append(dto.WeightEntries, privatePetWeightEntryDTO{
			ID: item.ID, Date: item.Date, WeightKg: item.WeightKg, Notes: item.Notes,
		})
	}
	return dto
}
