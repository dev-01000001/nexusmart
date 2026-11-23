package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

type usrFixShopDTO struct {
	ID                 string `json:"id"`
	OwnerUserID        string `json:"owner_user_id"`
	Name               string `json:"name"`
	Slug               string `json:"slug"`
	Description        string `json:"description"`
	PickupAddressID    string `json:"pickup_address_id"`
	ContactPhone       string `json:"contact_phone"`
	BusinessLicenseDoc string `json:"business_license_doc"`
	Status             string `json:"status"`
	RejectionReason    string `json:"rejection_reason"`
}

type usrFixShopResponse struct {
	Shop usrFixShopDTO `json:"shop"`
}

type usrFixUpdateAddressRequest struct {
	RecipientName  string `json:"recipient_name,omitempty"`
	RecipientPhone string `json:"recipient_phone,omitempty"`
	ProvinceCode   string `json:"province_code,omitempty"`
	ProvinceName   string `json:"province_name,omitempty"`
	WardCode       string `json:"ward_code,omitempty"`
	WardName       string `json:"ward_name,omitempty"`
	StreetAddress  string `json:"street_address,omitempty"`
}

type usrFixUpdateMeRequest struct {
	FullName string `json:"full_name,omitempty"`
	Phone    string `json:"phone,omitempty"`
}

type usrFixUserResponse struct {
	User UserDTO `json:"user"`
}

func TestUserRulesWave1(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	prov, ward := env.ensureProvinceAndWard(t)

	// 1. Register user, verify email OTP -> phone_verified must be false (USR-B7).
	u := env.registerVerifyAndLogin(t, "usrfix")
	if u.User.PhoneVerified {
		t.Fatalf("USR-B7: expected phone_verified=false after email OTP verification, got true")
	}

	// Changing phone via PATCH /users/me keeps phone_verified=false.
	newPhone := env.uniquePhone()
	patchResp := env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", u.AccessToken, nil, usrFixUpdateMeRequest{
		Phone: newPhone,
	}, http.StatusOK)
	var patchedUser usrFixUserResponse
	if err := DecodeJSON(patchResp, &patchedUser); err != nil {
		t.Fatalf("decode PATCH /users/me: %v", err)
	}
	if patchedUser.User.Phone != newPhone || patchedUser.User.PhoneVerified {
		t.Fatalf("USR-B7: want phone=%s phone_verified=false, got phone=%s phone_verified=%v",
			newPhone, patchedUser.User.Phone, patchedUser.User.PhoneVerified)
	}
	u.Phone = newPhone

	// 2. USR-B6: Invalid province_code -> 400; invalid ward_code -> 400; invalid phone -> 400.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", u.AccessToken, nil, CreateAddressRequest{
		RecipientName:  "Nguoi Nhan Loi Tinh",
		RecipientPhone: u.Phone,
		ProvinceCode:   "INVALID_999",
		ProvinceName:   "Tinh Ao",
		WardCode:       "FAKE_WARD_000",
		WardName:       "Phuong Ao",
		StreetAddress:  "Dia chi ao",
		IsDefault:      false,
	}, http.StatusBadRequest)

	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", u.AccessToken, nil, CreateAddressRequest{
		RecipientName:  "Nguoi Nhan Loi Phuong",
		RecipientPhone: u.Phone,
		ProvinceCode:   prov.Code,
		ProvinceName:   prov.Name,
		WardCode:       "FAKE_WARD_000",
		WardName:       "Phuong Ao",
		StreetAddress:  "Dia chi ao",
		IsDefault:      false,
	}, http.StatusBadRequest)

	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", u.AccessToken, nil, CreateAddressRequest{
		RecipientName:  "Nguoi Nhan Loi SDT",
		RecipientPhone: "NOT-A-PHONE",
		ProvinceCode:   prov.Code,
		ProvinceName:   prov.Name,
		WardCode:       ward.Code,
		WardName:       ward.Name,
		StreetAddress:  "123 Le Loi",
		IsDefault:      false,
	}, http.StatusBadRequest)

	// Valid default address + PUT omitting is_default keeps is_default=true.
	addr := env.addAddress(t, u)
	if !addr.IsDefault {
		t.Fatalf("expected newly created default address to have is_default=true")
	}
	putPath := fmt.Sprintf("/api/v1/users/me/addresses/%s", url.PathEscape(addr.ID))
	putResp := env.mustDoJSON(t, http.MethodPut, putPath, u.AccessToken, nil, usrFixUpdateAddressRequest{
		StreetAddress: "So 22 duong Xuan Thuy",
	}, http.StatusOK)
	var updatedAddr CreateAddressResponse
	if err := DecodeJSON(putResp, &updatedAddr); err != nil {
		t.Fatalf("decode PUT address: %v", err)
	}
	if !updatedAddr.Address.IsDefault {
		t.Fatalf("USR-B6: PUT omitting is_default reset is_default to false")
	}

	// 3. USR-B2: Register shop missing required fields -> 400; valid register -> REQUEST_INFO -> resubmit same name -> PENDING.
	shopName := fmt.Sprintf("UsrFixShop-%s-%d", env.runID, env.nextSeq())
	env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name: shopName,
	}, http.StatusBadRequest)

	regShopResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "Ho so lan 1",
		PickupAddressID:    addr.ID,
		ContactPhone:       u.Phone,
		BusinessLicenseDoc: "GPKD-V1.pdf",
	}, http.StatusCreated, http.StatusOK)
	var regShop usrFixShopResponse
	if err := DecodeJSON(regShopResp, &regShop); err != nil {
		t.Fatalf("decode register shop: %v", err)
	}
	shopID := regShop.Shop.ID

	reviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/review", url.PathEscape(shopID))

	// REQUEST_INFO without reason -> 400.
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "REQUEST_INFO",
		Reason:   "",
	}, http.StatusBadRequest)

	// REQUEST_INFO with reason -> 200.
	reqInfoResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "REQUEST_INFO",
		Reason:   "Bo sung ban scan GPKD",
	}, http.StatusOK)
	var reqInfoOut usrFixShopResponse
	if err := DecodeJSON(reqInfoResp, &reqInfoOut); err != nil {
		t.Fatalf("decode REQUEST_INFO: %v", err)
	}
	if reqInfoOut.Shop.RejectionReason != "Bo sung ban scan GPKD" {
		t.Fatalf("expected rejection_reason recorded on REQUEST_INFO, got %+v", reqInfoOut.Shop)
	}

	// Resubmit via POST /api/v1/shops/register reusing the SAME shop name -> 201/200, same shop ID, PENDING, reason cleared.
	resubResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "Ho so da bo sung",
		PickupAddressID:    addr.ID,
		ContactPhone:       u.Phone,
		BusinessLicenseDoc: "GPKD-V2.pdf",
	}, http.StatusCreated, http.StatusOK)
	var resubOut usrFixShopResponse
	if err := DecodeJSON(resubResp, &resubOut); err != nil {
		t.Fatalf("decode resubmit shop: %v", err)
	}
	if resubOut.Shop.ID != shopID || !StatusEquals(resubOut.Shop.Status, "PENDING") || resubOut.Shop.RejectionReason != "" {
		t.Fatalf("USR-B2: unexpected resubmitted shop: %+v", resubOut.Shop)
	}

	// REJECT without reason -> 400; REJECT with reason -> REJECTED; resubmit same name -> PENDING.
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "REJECT",
		Reason:   "",
	}, http.StatusBadRequest)

	rejResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "REJECT",
		Reason:   "GPKD het han",
	}, http.StatusOK)
	var rejOut usrFixShopResponse
	if err := DecodeJSON(rejResp, &rejOut); err != nil {
		t.Fatalf("decode REJECT shop: %v", err)
	}
	if !StatusEquals(rejOut.Shop.Status, "REJECTED") {
		t.Fatalf("expected REJECTED, got %q", rejOut.Shop.Status)
	}

	resub2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "Ho so nop lai sau REJECT",
		PickupAddressID:    addr.ID,
		ContactPhone:       u.Phone,
		BusinessLicenseDoc: "GPKD-V3.pdf",
	}, http.StatusCreated, http.StatusOK)
	var resub2Out usrFixShopResponse
	if err := DecodeJSON(resub2Resp, &resub2Out); err != nil {
		t.Fatalf("decode resubmit after REJECT: %v", err)
	}
	if resub2Out.Shop.ID != shopID || !StatusEquals(resub2Out.Shop.Status, "PENDING") || resub2Out.Shop.RejectionReason != "" {
		t.Fatalf("USR-B2: unexpected shop after REJECT resubmit: %+v", resub2Out.Shop)
	}

	// Admin approves shop -> ACTIVE.
	approveResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "APPROVE",
		Reason:   "Du ho so",
	}, http.StatusOK)
	var approveOut usrFixShopResponse
	if err := DecodeJSON(approveResp, &approveOut); err != nil {
		t.Fatalf("decode APPROVE shop: %v", err)
	}
	if !StatusEquals(approveOut.Shop.Status, "ACTIVE") {
		t.Fatalf("expected ACTIVE after approve, got %q", approveOut.Shop.Status)
	}

	// 4. USR-B3: REJECT an ACTIVE shop -> 409 Conflict.
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "REJECT",
		Reason:   "Khong duoc reject shop dang hoat dong",
	}, http.StatusConflict)

	// 5. USR-B6: Deleting pickup_address_id of ACTIVE shop -> 409 Conflict.
	delPath := fmt.Sprintf("/api/v1/users/me/addresses/%s", url.PathEscape(addr.ID))
	env.mustDoJSON(t, http.MethodDelete, delPath, u.AccessToken, nil, nil, http.StatusConflict)
}
