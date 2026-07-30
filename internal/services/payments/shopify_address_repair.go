package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/shopify"
	"gorm.io/gorm"
)

const defaultShopifyAddressRepairLimit = 100

// RepairMappedShopifyOrderAddresses repairs legacy Shopify orders that were
// created before Pawrd persisted the shipping address in Shopify. It is safe to
// run repeatedly because Shopify is only updated after FetchOrder confirms that
// the order still has no shipping address.
func RepairMappedShopifyOrderAddresses(
	ctx context.Context,
	db *gorm.DB,
	admin shopify.AdminOrderClient,
	limit int,
) (int, error) {
	if db == nil || admin == nil {
		return 0, nil
	}
	updater, ok := admin.(shopify.AdminOrderAddressClient)
	if !ok {
		return 0, nil
	}
	if limit <= 0 {
		limit = defaultShopifyAddressRepairLimit
	}

	var orders []models.ShopOrder
	if err := db.WithContext(ctx).
		Where("shopify_order_id IS NOT NULL AND shopify_order_id <> ''").
		Order("updated_at DESC").
		Limit(limit).
		Find(&orders).Error; err != nil {
		return 0, fmt.Errorf("load mapped Shopify orders for address repair: %w", err)
	}

	repaired := 0
	var repairErrors []error
	for _, order := range orders {
		if strings.TrimSpace(order.CustomerName) == "" ||
			strings.TrimSpace(order.CustomerPhone) == "" ||
			strings.TrimSpace(order.ShippingAddress1) == "" ||
			strings.TrimSpace(order.ShippingDistrict) == "" ||
			strings.TrimSpace(order.ShippingRegion) == "" {
			continue
		}
		orderID := order.ShopifyOrderGID()
		snapshot, err := admin.FetchOrder(ctx, orderID)
		if err != nil {
			repairErrors = append(
				repairErrors,
				fmt.Errorf("fetch Shopify order %s: %w", order.ID, err),
			)
			continue
		}
		if snapshot == nil {
			repairErrors = append(
				repairErrors,
				fmt.Errorf("fetch Shopify order %s returned no snapshot", order.ID),
			)
			continue
		}
		if snapshot.HasShippingAddress {
			continue
		}
		if err := updater.UpdateOrderShippingAddress(
			ctx,
			orderID,
			shopify.AdminShippingAddressInput{
				Name: order.CustomerName, Phone: order.CustomerPhone,
				Address: order.ShippingAddress1, City: order.ShippingDistrict,
				Region: order.ShippingRegion,
			},
		); err != nil {
			repairErrors = append(
				repairErrors,
				fmt.Errorf("repair Shopify order %s shipping address: %w", order.ID, err),
			)
			continue
		}
		repaired++
	}
	return repaired, errors.Join(repairErrors...)
}
