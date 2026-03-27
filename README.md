# FinTrack — AI-Powered Personal Finance Manager

Хиймэл оюун ухаанд суурилсан хувийн санхүүгийн хяналт, зөвлөмжийн систем.

Diploma project — AI-based personal finance tracking and recommendation system.

## Tech Stack

| Layer     | Technology                      |
| --------- | ------------------------------- |
| Frontend  | React Native (Expo) + NativeWind (Tailwind CSS) |
| Backend   | Go (Gin framework + GORM)       |
| Database  | PostgreSQL 16                   |
| AI        | Built-in financial advisor engine |
| Infra     | Docker + Docker Compose         |

## Project Structure

```
.
├── backend/                  # Go backend API
│   ├── cmd/server/           # Entry point
│   ├── internal/
│   │   ├── config/           # Environment config
│   │   ├── database/         # DB connection & migrations
│   │   ├── handlers/         # HTTP handlers (controllers)
│   │   ├── middleware/       # Auth, CORS middleware
│   │   ├── models/           # Data models & DTOs
│   │   └── routes/           # API route definitions
│   ├── Dockerfile
│   └── .env
├── frontend/                 # React Native app
│   ├── src/
│   │   ├── screens/          # App screens
│   │   ├── components/       # Reusable components
│   │   ├── navigation/       # Navigation config
│   │   ├── services/         # API service layer
│   │   └── context/          # Auth context
│   ├── tailwind.config.js
│   └── App.js
├── docs/                     # Documentation
│   └── work.md               # Work log
├── docker-compose.yml        # Docker orchestration
└── README.md
```

## Getting Started

### Prerequisites

- Docker & Docker Compose
- Node.js 18+
- Expo CLI (`npm install -g expo-cli`)

### 1. Start Backend (Docker)

```bash
# Start PostgreSQL + Go backend
docker-compose up --build -d

# Backend runs on http://localhost:8080
```

### 2. Start Frontend

```bash
cd frontend
npm install
npx expo start
```

Expo QR кодоор утсан дээрээ Expo Go апп-аар нээнэ.

## API Endpoints

### Auth
- `POST /api/v1/auth/register` — Бүртгүүлэх
- `POST /api/v1/auth/login` — Нэвтрэх

### Dashboard
- `GET /api/v1/dashboard` — Нүүр хуудасны мэдээлэл
- `GET /api/v1/expenses/summary` — Зарлагын нэгтгэл
- `GET /api/v1/statistics` — Статистик

### Transactions
- `GET /api/v1/transactions` — Гүйлгээний жагсаалт
- `POST /api/v1/transactions` — Шинэ гүйлгээ
- `DELETE /api/v1/transactions/:id` — Гүйлгээ устгах

### Budgets
- `GET /api/v1/budgets` — Төсвийн жагсаалт
- `POST /api/v1/budgets` — Шинэ төсөв
- `PUT /api/v1/budgets/:id` — Төсөв шинэчлэх

### AI Chat
- `POST /api/v1/ai/chat` — AI зөвлөгөө авах
- `GET /api/v1/ai/chats` — Чатын түүх

## Features

- **Dashboard**: Нийт баланс, орлого/зарлагын нэгтгэл, хадгаламжийн хувь
- **Transaction Tracking**: Гүйлгээ бүртгэх, хайх, шүүх
- **Expense Analytics**: Зарлагын категори, хувь хэмжээ, donut chart
- **Budget Management**: Сарын төсөв тогтоох, хяналт
- **Statistics**: Daily/Weekly/Monthly/Yearly статистик, bar chart
- **AI Financial Advisor**: Хиймэл оюун ухаанд суурилсан санхүүгийн зөвлөмж
- **Scheduled Payments**: Давтагдах төлбөрүүд
- **Dark Theme UI**: Figma дизайн дагуу бүрэн dark theme
