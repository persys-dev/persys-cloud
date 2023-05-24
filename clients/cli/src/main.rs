use std::borrow::Cow;
use std::borrow::Cow::Owned;
use reqwest::{ClientBuilder, Error, redirect, Url};
use std::collections::HashMap;
use std::time::Duration;
use std::fs::File;
use tokio;
use serde::{Deserialize, Serialize};
use std::path::Path;
use std::string::ToString;
use figlet_rs::FIGfont;
use rustyline::{Config, Context, Editor, Helper};
use crossterm::style::Stylize;
use rustyline::completion::{Candidate, Completer as ThisCompleter, Pair};
use rustyline::error::ReadlineError;
use rustyline::highlight::Highlighter;
use rustyline::hint::Hinter;
use rustyline::history::DefaultHistory;
use rustyline::validate::Validator;
use snailshell::snailprint;

static mut tkn: String = String::new();

#[derive(Deserialize)]
struct AuthUser {
    login: String,
    name: String,
    email: String,
    company: String,
    url: String,
    githubToken: String,
    userID: std::primitive::i64,
    persysToken: String,
    state: String,
    createdAt: String,
    updatedAt: String,
}

struct MyCompleter;

impl ThisCompleter for MyCompleter {
    type Candidate = Pair;

    fn complete(&self, line: &str, pos: usize, ctx: &Context<'_>) -> rustyline::Result<(usize, Vec<Self::Candidate>)> {
        let commands = vec!["login", "add_webhook", "add_access_token", "list_pipeline", "list_repos", "events"]; // Replace this with your custom set of commands
            let candidates = commands
                .iter()
                .filter(|cmd| cmd.starts_with(line))
                .map(|cmd| Pair {
                    display: cmd.to_string(),
                    replacement: cmd[pos..].to_string(),
                })
                .collect();
        Ok((pos, candidates))
    }

}

impl Helper for MyCompleter {}

impl Validator for MyCompleter{}

impl Highlighter for MyCompleter{}

impl Hinter for MyCompleter { type Hint = String; }

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let config = Config::builder().auto_add_history(true).build();
    let mut rl: Editor<MyCompleter, DefaultHistory> = Editor::with_config(config)?;
    // rl.set_completer(Some(MyCompleter {}));
    let standard_font = FIGfont::standard().unwrap();
    let figure = standard_font.convert("Shipper-cli!");
    assert!(figure.is_some());
    println!("{}", figure.unwrap());
    snailprint("Welcome To Persys Developer Platform Client !".magenta());
    loop {
        if let Ok(line) = rl.readline("> ") {
            let trimmed = line.trim();
            if trimmed == "login" {
                match login().await {
                    Ok(user_data) => unsafe {
                        tkn = user_data.persysToken;
                        println!("Welcome, {}", user_data.name);
                        break;
                    },
                    Err(e) => {
                        println!("Error during login: {}", e);
                    }
                }
            } else if trimmed == "add_webhook" {
                unsafe {
                    if tkn == "" {
                        println!("you need to login first!")
                    }
                }
                let token = rl.readline("Access token: ").unwrap().trim().to_string();
                let repo_id = rl.readline("Repository Name: ").unwrap().trim().to_string();
                match add_webhook(token, repo_id).await {
                    Ok(_) => {
                        println!("Webhook added successfully.");
                        break;
                    },
                    Err(e) => {
                        println!("Error adding webhook: {}", e);
                    }
                }
            } else if trimmed == "add_access_token" {
                let token = rl.readline("Token: ").unwrap().trim().to_string();
                let access_token = rl.readline("Access Token: ").unwrap().trim().to_string();
                match add_access_token(token, access_token).await {
                    Ok(_) => {
                        println!("Access token added successfully.");
                        break;
                    },
                    Err(e) => {
                        println!("Error adding access token: {}", e);
                    }
                }
            } else if trimmed == "exit" {
                break;
            } else {
                println!("Invalid command!");
            }
        } else {
            break;
        }
    }
    Ok(())
}
async fn login() -> reqwest::Result<AuthUser> {
    let body = reqwest::get("http://localhost:8551/auth/login").await?
        .json::<HashMap<String, String>>()
        .await?;
    let url_str = body.get("URL").unwrap().trim().to_string();
    open::that(&url_str).unwrap();
    let url = Url::parse(&url_str).unwrap();
    let params = url.query_pairs().collect::<HashMap<_, _>>().get("state").unwrap().to_string();
    snailprint("Please complete the login process within 30 seconds!");
    let sec = Duration::from_secs(30);
    tokio::time::sleep(sec).await;
    // let params = Url::parse(&data).unwrap().query_pairs().collect::<HashMap<_, _>>().get("state").unwrap().to_string();
    let mut map = HashMap::new();
    map.insert("state", &params);
    let url = "http://localhost:8551/auth/cli";
    let res = reqwest::Client::new()
        .post(url)
        .json(&map)
        .send()
        .await?
        .json::<AuthUser>()
        .await?;
    return Ok(res);
}
async fn add_webhook(token: String, repo_name: String) -> reqwest::Result<()> {
    println!("{}", repo_name);
    return Ok(());
}
async fn add_access_token(token: String , access_token: String) -> reqwest::Result<()> {
    return Ok(());
}