package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/shopify"
)

type addressRepairAdmin struct {
	snapshots map[string]*shopify.AdminOrderSnapshot
	fetchErrs map[string]error
	updates   map[string]shopify.AdminShippingAddressInput
}

func (a *addressRepairAdmin) CreateOrder(
	context.Context,
	shopify.AdminOrderInput,
) (*shopify.AdminOrderResult, error) {
	return nil, nil
}

func (a *addressRepairAdmin) FetchOrder(
	_ context.Context,
	orderID string,
) (*shopify.AdminOrderSnapshot, error) {
	if err := a.fetchErrs[orderID]; err != nil {
		return nil, err
	}
	return a.snapshots[orderID], nil
}

func (a *addressRepairAdmin) AddOrderTags(context.Context, string, []string) error {
	return nil
}

func (a *addressRepairAdmin) RequestReturn(
	context.Context,
	string,
	string,
	string,
) (*shopify.AdminReturnResult, error) {
	return nil, nil
}

func (a *addressRepairAdmin) UpdateOrderShippingAddress(
	_ context.Context,
	orderID string,
	input shopify.AdminShippingAddressInput,
) error {
	a.updates[orderID] = input
	return nil
}

func TestRepairMappedShopifyOrderAddresses(t *testing.T) {
	db := newFulfillmentTestDB(t)
	missingAddressID := "gid://shopify/Order/1004"
	existingAddressID := "gid://shopify/Order/1005"
	incompleteAddressID := "gid://shopify/Order/1006"
	orders := []models.ShopOrder{
		{
			ID: "order-1004", UserID: "user-1", PaymentIntentID: testStringPointer("pi_1004"),
			ShopifyOrderID: &missingAddressID, Status: "processing",
			FinancialStatus: "paid", Currency: "HKD", TotalAmountMinor: 9642,
			CustomerName: "Alice Wong", CustomerPhone: "+852 9123 4567",
			ShippingAddress1: "1 Test Street", ShippingDistrict: "Central",
			ShippingRegion: "Hong Kong Island",
		},
		{
			ID: "order-1005", UserID: "user-1", PaymentIntentID: testStringPointer("pi_1005"),
			ShopifyOrderID: &existingAddressID, Status: "processing",
			FinancialStatus: "paid", Currency: "HKD", TotalAmountMinor: 1000,
			CustomerName: "Bob Chan", CustomerPhone: "+852 9234 5678",
			ShippingAddress1: "2 Test Street", ShippingDistrict: "Kowloon City",
			ShippingRegion: "Kowloon",
		},
		{
			ID: "order-1006", UserID: "user-1", PaymentIntentID: testStringPointer("pi_1006"),
			ShopifyOrderID: &incompleteAddressID, Status: "processing",
			FinancialStatus: "paid", Currency: "HKD", TotalAmountMinor: 1000,
			CustomerName: "No Phone", ShippingAddress1: "3 Test Street",
			ShippingDistrict: "Sha Tin", ShippingRegion: "New Territories",
		},
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatal(err)
	}
	admin := &addressRepairAdmin{
		snapshots: map[string]*shopify.AdminOrderSnapshot{
			missingAddressID:  {HasShippingAddress: false},
			existingAddressID: {HasShippingAddress: true},
		},
		fetchErrs: map[string]error{},
		updates:   map[string]shopify.AdminShippingAddressInput{},
	}

	repaired, err := RepairMappedShopifyOrderAddresses(
		context.Background(),
		db,
		admin,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if repaired != 1 {
		t.Fatalf("repaired=%d, want 1", repaired)
	}
	if len(admin.updates) != 1 {
		t.Fatalf("updates=%d, want 1", len(admin.updates))
	}
	update := admin.updates[missingAddressID]
	if update.Name != "Alice Wong" ||
		update.Phone != "+852 9123 4567" ||
		update.Address != "1 Test Street" ||
		update.City != "Central" ||
		update.Region != "Hong Kong Island" {
		t.Fatalf("unexpected address update: %+v", update)
	}
	if _, fetched := admin.snapshots[incompleteAddressID]; fetched {
		t.Fatal("test fixture must not provide an incomplete-order snapshot")
	}
}

func TestRepairMappedShopifyOrderAddressesContinuesAfterFetchFailure(t *testing.T) {
	db := newFulfillmentTestDB(t)
	failingID := "gid://shopify/Order/failing"
	repairableID := "gid://shopify/Order/repairable"
	for _, order := range []models.ShopOrder{
		{
			ID: "failing", UserID: "user-1", PaymentIntentID: testStringPointer("pi_failing"),
			ShopifyOrderID: &failingID, Status: "processing",
			FinancialStatus: "paid", Currency: "HKD", TotalAmountMinor: 1000,
			CustomerName: "Failure", CustomerPhone: "111",
			ShippingAddress1: "1 Failure Street", ShippingDistrict: "Central",
			ShippingRegion: "Hong Kong Island",
		},
		{
			ID: "repairable", UserID: "user-1", PaymentIntentID: testStringPointer("pi_repairable"),
			ShopifyOrderID: &repairableID, Status: "processing",
			FinancialStatus: "paid", Currency: "HKD", TotalAmountMinor: 1000,
			CustomerName: "Repairable", CustomerPhone: "222",
			ShippingAddress1: "2 Repair Street", ShippingDistrict: "Central",
			ShippingRegion: "Hong Kong Island",
		},
	} {
		if err := db.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
	}
	admin := &addressRepairAdmin{
		snapshots: map[string]*shopify.AdminOrderSnapshot{
			repairableID: {HasShippingAddress: false},
		},
		fetchErrs: map[string]error{
			failingID: errors.New("temporary Shopify failure"),
		},
		updates: map[string]shopify.AdminShippingAddressInput{},
	}

	repaired, err := RepairMappedShopifyOrderAddresses(
		context.Background(),
		db,
		admin,
		10,
	)
	if repaired != 1 {
		t.Fatalf("repaired=%d, want 1", repaired)
	}
	if err == nil {
		t.Fatal("expected the fetch failure to be reported")
	}
	if _, ok := admin.updates[repairableID]; !ok {
		t.Fatal("repairable order was not updated after another order failed")
	}
}
