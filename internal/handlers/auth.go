package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fintrack-backend/internal/config"
	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AuthHandler struct {
	DB  *gorm.DB
	Cfg *config.Config
}

func NewAuthHandler(db *gorm.DB, cfg *config.Config) *AuthHandler {
	return &AuthHandler{DB: db, Cfg: cfg}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var existing models.User
	if err := h.DB.Where("email = ?", req.Email).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Email already registered"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	user := models.User{
		Name:     req.Name,
		Email:    req.Email,
		Password: string(hashedPassword),
		Currency: "USD",
	}

	if err := h.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	// Create default account
	account := models.Account{
		UserID:  user.ID,
		Name:    "Main Account",
		Type:    "bank",
		Balance: 0,
		Icon:    "wallet",
		Color:   "#4ECDC4",
	}
	h.DB.Create(&account)

	token, err := h.generateToken(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusCreated, models.AuthResponse{Token: token, User: user})
	LogActivity(h.DB, user.ID, "register", "user", user.ID, "", "success", c.ClientIP())
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	if err := h.DB.Where("email = ?", req.Email).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
		return
	}

	token, err := h.generateToken(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, models.AuthResponse{Token: token, User: user})
	LogActivity(h.DB, user.ID, "login", "user", user.ID, "", "success", c.ClientIP())
}

func (h *AuthHandler) GetSocialProviders(c *gin.Context) {
	providers := []models.SocialProviderStatus{
		{
			Provider:   "google",
			Label:      "Google",
			Enabled:    h.Cfg.GoogleIOSClientID != "" || h.Cfg.GoogleWebClientID != "",
			Configured: h.Cfg.GoogleIOSClientID != "" || h.Cfg.GoogleWebClientID != "",
			Hint:       "GOOGLE_IOS_CLIENT_ID эсвэл GOOGLE_WEB_CLIENT_ID шаардлагатай",
		},
		{
			Provider:   "facebook",
			Label:      "Facebook",
			Enabled:    h.Cfg.FacebookAppID != "",
			Configured: h.Cfg.FacebookAppID != "",
			Hint:       "FACEBOOK_APP_ID шаардлагатай",
		},
		{
			Provider:   "apple",
			Label:      "Apple",
			Enabled:    h.Cfg.AppleBundleID != "",
			Configured: h.Cfg.AppleBundleID != "",
			Hint:       "APPLE_BUNDLE_ID шаардлагатай",
		},
	}

	c.JSON(http.StatusOK, providers)
}

func (h *AuthHandler) SocialLogin(c *gin.Context) {
	var req models.SocialAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if !h.isSocialProviderConfigured(provider) {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": h.socialProviderSetupHint(provider),
		})
		return
	}

	c.JSON(http.StatusNotImplemented, gin.H{
		"error": fmt.Sprintf("%s social login backend scaffold бэлэн байна. Одоо provider SDK token verify болон redirect/app id холболтыг үргэлжлүүлэх шаардлагатай.", strings.Title(provider)),
	})
}

func (h *AuthHandler) GetProfile(c *gin.Context) {
	userID := c.GetUint("user_id")

	var user models.User
	if err := h.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, user)
}

func (h *AuthHandler) UpdateProfile(c *gin.Context) {
	userID := c.GetUint("user_id")

	var user models.User
	if err := h.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	var input struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Avatar   string `json:"avatar"`
		Currency string `json:"currency"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if input.Name != "" {
		user.Name = input.Name
	}
	if input.Email != "" {
		// Check if email is taken by another user
		var existing models.User
		if err := h.DB.Where("email = ? AND id != ?", input.Email, user.ID).First(&existing).Error; err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "Email already in use"})
			return
		}
		user.Email = input.Email
	}
	if input.Phone != "" {
		user.Phone = input.Phone
	}
	if input.Avatar != "" {
		user.Avatar = input.Avatar
	}
	if input.Currency != "" {
		user.Currency = input.Currency
	}

	h.DB.Save(&user)
	c.JSON(http.StatusOK, user)
}

func (h *AuthHandler) ChangePassword(c *gin.Context) {
	userID := c.GetUint("user_id")

	var user models.User
	if err := h.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	var input struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.OldPassword)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Хуучин нууц үг буруу байна"})
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	user.Password = string(hashed)
	h.DB.Save(&user)
	c.JSON(http.StatusOK, gin.H{"message": "Нууц үг амжилттай солигдлоо"})
}

func (h *AuthHandler) UploadAvatar(c *gin.Context) {
	userID := c.GetUint("user_id")

	var user models.User
	if err := h.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	file, err := c.FormFile("avatar")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Зураг оруулна уу"})
		return
	}

	dir := "./uploads/avatars"
	os.MkdirAll(dir, 0755)

	ext := filepath.Ext(file.Filename)
	filename := fmt.Sprintf("avatar_%d_%d%s", userID, time.Now().Unix(), ext)
	path := filepath.Join(dir, filename)

	if err := c.SaveUploadedFile(file, path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Зураг хадгалж чадсангүй"})
		return
	}

	user.Avatar = "/uploads/avatars/" + filename
	h.DB.Save(&user)
	c.JSON(http.StatusOK, user)
}

func (h *AuthHandler) generateToken(userID uint) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(72 * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.Cfg.JWTSecret))
}

func (h *AuthHandler) isSocialProviderConfigured(provider string) bool {
	switch provider {
	case "google":
		return h.Cfg.GoogleIOSClientID != "" || h.Cfg.GoogleWebClientID != ""
	case "facebook":
		return h.Cfg.FacebookAppID != ""
	case "apple":
		return h.Cfg.AppleBundleID != ""
	default:
		return false
	}
}

func (h *AuthHandler) socialProviderSetupHint(provider string) string {
	switch provider {
	case "google":
		return "Google login ашиглахын тулд backend env дээр GOOGLE_IOS_CLIENT_ID эсвэл GOOGLE_WEB_CLIENT_ID тохируулна уу."
	case "facebook":
		return "Facebook login ашиглахын тулд backend env дээр FACEBOOK_APP_ID тохируулна уу."
	case "apple":
		return "Apple login ашиглахын тулд backend env дээр APPLE_BUNDLE_ID тохируулна уу."
	default:
		return "Social login provider тохиргоо дутуу байна."
	}
}
