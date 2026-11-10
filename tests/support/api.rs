use std::sync::{Arc, Mutex};

use serde_json::Value;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::TcpListener;

pub struct Seen {
    pub target: String,
    pub auth: String,
}

type Routes = Arc<Mutex<Vec<(String, Value)>>>;

/// A read API on loopback. A route pairs a request target (path and query) with its JSON answer,
/// and a `{base}` in an answer becomes the server's own origin, so a page can link to the next.
pub struct Api {
    pub origin: String,
    pub seen: Arc<Mutex<Vec<Seen>>>,
    routes: Routes,
}

impl Api {
    pub async fn serve(routes: Vec<(&str, Value)>) -> Api {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let origin = format!("http://{}", listener.local_addr().unwrap());
        let api = Api {
            origin: origin.clone(),
            seen: Arc::default(),
            routes: Arc::default(),
        };
        for (target, answer) in routes {
            api.route(target, answer);
        }
        let (log, routes) = (api.seen.clone(), api.routes.clone());
        tokio::spawn(async move {
            loop {
                let (socket, _) = listener.accept().await.unwrap();
                let (log, routes, origin) = (log.clone(), routes.clone(), origin.clone());
                tokio::spawn(async move {
                    let mut socket = BufReader::new(socket);
                    loop {
                        let mut request_line = String::new();
                        if socket.read_line(&mut request_line).await.unwrap_or(0) == 0 {
                            return;
                        }
                        let target = request_line.split(' ').nth(1).unwrap_or("").to_string();
                        let mut auth = String::new();
                        loop {
                            let mut header = String::new();
                            socket.read_line(&mut header).await.unwrap();
                            let header = header.trim_end();
                            if header.is_empty() {
                                break;
                            }
                            let (name, value) = header.split_once(':').unwrap();
                            if name.eq_ignore_ascii_case("authorization") {
                                auth = value.trim().to_string();
                            }
                        }
                        let found = routes
                            .lock()
                            .unwrap()
                            .iter()
                            .find(|(t, _)| *t == target)
                            .map(|(_, answer)| answer.to_string().replace("{base}", &origin));
                        log.lock().unwrap().push(Seen { target, auth });
                        let reply = match found {
                            Some(body) => format!(
                                "HTTP/1.1 200 OK\r\ncontent-type: application/json\r\ncontent-length: {}\r\n\r\n{body}",
                                body.len()
                            ),
                            None => {
                                "HTTP/1.1 404 Not Found\r\ncontent-length: 0\r\n\r\n".to_string()
                            }
                        };
                        socket.get_mut().write_all(reply.as_bytes()).await.unwrap();
                    }
                });
            }
        });
        api
    }

    /// Adds a route, or replaces the answer to one that exists.
    pub fn route(&self, target: &str, answer: Value) {
        let mut routes = self.routes.lock().unwrap();
        routes.retain(|(t, _)| t != target);
        routes.push((target.to_string(), answer));
    }

    pub fn targets(&self) -> Vec<String> {
        self.seen
            .lock()
            .unwrap()
            .iter()
            .map(|s| s.target.clone())
            .collect()
    }
}
