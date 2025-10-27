package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/BurntSushi/toml"
	_ "github.com/lib/pq"
)

type Config struct {
	Master  Database   `toml:"master"`
	Replica []Database `toml:"replica"`
	PgCat   Database   `toml:"pgcat"`
}

type Database struct {
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	User     string `toml:"user"`
	Password string `toml:"password"`
	Database string `toml:"database"`
}

type Chat struct {
	ID        int       `json:"id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type Server struct {
	master  *sql.DB
	replica *sql.DB
	pgcat   *sql.DB
}

func main() {
	// Read config file
	var config Config
	if _, err := toml.DecodeFile("config.toml", &config); err != nil {
		log.Fatal("Failed to read config.toml:", err)
	}

	// Connect to master database
	masterConn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Master.Host,
		config.Master.Port,
		config.Master.User,
		config.Master.Password,
		config.Master.Database,
	)

	master, err := sql.Open("postgres", masterConn)
	if err != nil {
		log.Fatal("Failed to connect to master DB:", err)
	}
	defer master.Close()

	// Connect to replica database
	if len(config.Replica) == 0 {
		log.Fatal("No replica database configured")
	}

	replicaDB := config.Replica[0]
	replicaConn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		replicaDB.Host,
		replicaDB.Port,
		config.Master.User,
		config.Master.Password,
		config.Master.Database,
	)

	replica, err := sql.Open("postgres", replicaConn)
	if err != nil {
		log.Fatal("Failed to connect to replica DB:", err)
	}
	defer replica.Close()

	log.Println("replica DB connected")

	// Connect to PgCat
	pgcatConn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.PgCat.Host,
		config.PgCat.Port,
		config.Master.User,
		config.Master.Password,
		config.Master.Database,
	)

	pgcat, err := sql.Open("postgres", pgcatConn)

	log.Println("cat DB connected")
	if err != nil {
		log.Fatal("Failed to connect to PgCat:", err)
	}
	defer pgcat.Close()

	// Test connections
	if err := master.Ping(); err != nil {
		log.Fatal("Master DB ping failed:", err)
	}
	if err := replica.Ping(); err != nil {
		log.Fatal("Replica DB ping failed:", err)
	}
	if err := pgcat.Ping(); err != nil {
		log.Fatal("PgCat ping failed:", err)
	}

	log.Println("Connected to all databases successfully!")
	log.Printf("Master: %s:%d", config.Master.Host, config.Master.Port)
	log.Printf("Replica: %s:%d", replicaDB.Host, replicaDB.Port)
	log.Printf("PgCat: %s:%d", config.PgCat.Host, config.PgCat.Port)

	srv := &Server{master: master, replica: replica, pgcat: pgcat}

	// Routes
	http.HandleFunc("/", srv.handleHome)
	http.HandleFunc("/api/chats", srv.handleChats)
	http.HandleFunc("/api/compare", srv.handleCompare)

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	tmpl := `
<!DOCTYPE html>
<html>
<head>
	<title>PostgreSQL Replication Demo</title>
	<style>
		body { font-family: Arial, sans-serif; max-width: 1400px; margin: 0 auto; padding: 20px; }
		.add-section { margin-bottom: 20px; padding: 15px; background: #f8f9fa; border-radius: 5px; }
		.add-buttons { display: flex; gap: 10px; margin-top: 10px; }
		.container { display: flex; gap: 20px; margin-top: 20px; }
		.db-section { flex: 1; border: 1px solid #ddd; padding: 15px; border-radius: 5px; }
		.db-section h2 { margin-top: 0; }
		input, button { padding: 8px; margin: 5px 0; }
		button { background: #007bff; color: white; border: none; cursor: pointer; border-radius: 3px; }
		button:hover { background: #0056b3; }
		.btn-danger { background: #dc3545; }
		.btn-danger:hover { background: #c82333; }
		.btn-success { background: #28a745; }
		.btn-success:hover { background: #218838; }
		.chat-list { max-height: 400px; overflow-y: auto; margin-top: 10px; }
		.chat-item { background: #f8f9fa; padding: 10px; margin: 5px 0; border-radius: 3px; }
		.comparison { margin-top: 20px; padding: 15px; background: #e7f3ff; border-radius: 5px; }
		.match { color: green; font-weight: bold; }
		.mismatch { color: red; font-weight: bold; }
		.error { color: red; padding: 10px; background: #ffe6e6; border-radius: 3px; margin-top: 10px; }
		.success { color: green; padding: 10px; background: #e6ffe6; border-radius: 3px; margin-top: 10px; }
	</style>
</head>
<body>
	<h1>PostgreSQL Streaming Replication Demo</h1>
	<p>This demo shows data synchronization between PostgreSQL databases with PgCat load balancer.</p>
	
	<div class="add-section">
		<h3>Add New Chat Message</h3>
		<input type="text" id="message" placeholder="Enter message" style="width: 400px;">
		<div class="add-buttons">
			<button class="btn-success" onclick="addChat('master')">Add to Master (Should Work)</button>
			<button class="btn-danger" onclick="addChat('replica')">Add to Replica (Should Fail)</button>
			<button onclick="addChat('pgcat')">Add via PgCat (Load Balancer)</button>
		</div>
		<div id="result"></div>
	</div>

	<div class="container">
		<div class="db-section">
			<h2>Master Database</h2>
			<button onclick="loadChats('master')">Refresh</button>
			<div id="master-chats" class="chat-list"></div>
		</div>
		<div class="db-section">
			<h2>Replica Database</h2>
			<button onclick="loadChats('replica')">Refresh</button>
			<div id="replica-chats" class="chat-list"></div>
		</div>
		<div class="db-section">
			<h2>PgCat Load Balancer</h2>
			<button onclick="loadChats('pgcat')">Refresh</button>
			<div id="pgcat-chats" class="chat-list"></div>
		</div>
	</div>

	<div class="comparison">
		<h3>Replication Status</h3>
		<button onclick="compareDBs()">Compare Databases</button>
		<div id="comparison-result"></div>
	</div>

	<script>
		async function addChat(target) {
			const msg = document.getElementById('message').value;
			if (!msg) return alert('Please enter a message');
			
			const resultDiv = document.getElementById('result');
			resultDiv.innerHTML = '<p>Processing...</p>';
			
			try {
				const res = await fetch('/api/chats', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify({message: msg, target: target})
				});
				
				const data = await res.json();
				
				if (res.ok) {
					resultDiv.innerHTML = '<div class="success">✓ Success! Message added to ' + target + '</div>';
					document.getElementById('message').value = '';
					setTimeout(() => {
						loadChats('master');
						loadChats('replica');
						loadChats('pgcat');
					}, 100);
				} else {
					resultDiv.innerHTML = '<div class="error">✗ Error: ' + data.error + '</div>';
				}
			} catch (err) {
				resultDiv.innerHTML = '<div class="error">✗ Network error: ' + err.message + '</div>';
			}
		}

		async function loadChats(db) {
			const res = await fetch('/api/chats?db=' + db);
			const chats = await res.json();
			const container = document.getElementById(db + '-chats');
			
			if (chats && chats.length > 0) {
				container.innerHTML = chats.map(c => 
					'<div class="chat-item"><strong>#' + c.id + '</strong>: ' + c.message + 
					'<br><small>' + new Date(c.created_at).toLocaleString() + '</small></div>'
				).join('');
			} else {
				container.innerHTML = '<div class="chat-item">No messages yet</div>';
			}
		}

		async function compareDBs() {
			const res = await fetch('/api/compare');
			const data = await res.json();
			const result = document.getElementById('comparison-result');
			
			const allMatch = data.count1 === data.count2 && data.count1 === data.count3;
			result.innerHTML = '<p class="' + (allMatch ? 'match' : 'mismatch') + '">' +
				(allMatch ? '✓ All databases are in sync!' : '✗ Databases are NOT in sync') +
				'</p><p>Master: ' + data.count1 + ' | Replica: ' + data.count2 + ' | PgCat: ' + data.count3 + ' records</p>';
		}

		// Load initial data
		loadChats('master');
		loadChats('replica');
		loadChats('pgcat');
		compareDBs();
	</script>
</body>
</html>
	`
	w.Header().Set("Content-Type", "text/html")
	template.Must(template.New("home").Parse(tmpl)).Execute(w, nil)
}

func (s *Server) handleChats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case "GET":
		db := r.URL.Query().Get("db")
		var chats []Chat
		var dbConn *sql.DB

		switch db {
		case "replica":
			dbConn = s.replica
		case "pgcat":
			dbConn = s.pgcat
		default:
			dbConn = s.master
		}

		rows, err := dbConn.Query("SELECT id, message, created_at FROM chat ORDER BY id DESC")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var c Chat
			if err := rows.Scan(&c.ID, &c.Message, &c.CreatedAt); err != nil {
				continue
			}
			chats = append(chats, c)
		}

		json.NewEncoder(w).Encode(chats)

	case "POST":
		var req struct {
			Message string `json:"message"`
			Target  string `json:"target"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var dbConn *sql.DB
		switch req.Target {
		case "replica":
			dbConn = s.replica
		case "pgcat":
			dbConn = s.pgcat
		default:
			dbConn = s.master
		}

		var chat Chat
		err := dbConn.QueryRow(
			"INSERT INTO chat (message) VALUES ($1) RETURNING id, created_at",
			req.Message,
		).Scan(&chat.ID, &chat.CreatedAt)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": err.Error(),
			})
			return
		}

		chat.Message = req.Message
		json.NewEncoder(w).Encode(chat)
	}
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	var count1, count2, count3 int

	s.master.QueryRow("SELECT COUNT(*) FROM chat").Scan(&count1)
	s.replica.QueryRow("SELECT COUNT(*) FROM chat").Scan(&count2)
	s.pgcat.QueryRow("SELECT COUNT(*) FROM chat").Scan(&count3)

	result := map[string]interface{}{
		"count1": count1,
		"count2": count2,
		"count3": count3,
		"match":  count1 == count2 && count1 == count3,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
