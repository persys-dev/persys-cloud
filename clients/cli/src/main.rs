use reqwest::{ClientBuilder, Error, redirect, Url};
use std::collections::HashMap;
use std::iter::FromIterator;
use std::{thread, time};
use std::fs::File;
use tokio::{runtime,sync::mpsc::{channel, Receiver}};
use inquire::{
    error::{CustomUserError, InquireResult},
    required, CustomType, MultiSelect, Select, Text,
};
use inquire::formatter::StringFormatter;
// use tokio::signal::windows::ctrl_break;
use toml;
use serde::Deserialize;
use serde::Serialize;
use std::path::Path;
use figlet_rs::FIGfont;
use snailshell::*;
use crossterm::style::Stylize;

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

#[tokio::main]
// TODO After login initial setup and persistence
async fn main() -> Result<(), inquire::error::InquireError> {
    // showing the banner
    let standard_font = FIGfont::standard().unwrap();
    let figure = standard_font.convert("Shipper-cli!");
    assert!(figure.is_some());
    println!("{}", figure.unwrap());

    snailprint("Welcome To Persys Developer Platform Client !".magenta());
    // println!("Welcome To persys developer platform client!");
    let mut rs:bool=true;
    // checking for config file
    rs = Path::new("config.toml").exists();

    if rs == true{
        println!("config file found");
    }
    else{
        snailprint("Config File Not Found...");
        snailprint("Initializing Client...");
        snailprint("Logging You in... Opening Browser!");

        let user_data = login().await.unwrap();
        println!("Welcome {:#?}", user_data.name);

        let token = user_data.persysToken;

        // creating config file

        // (File::create("config.toml"))?;

        // let inputs = Select::new("Please select repos you want in your pipeline:", .prompt()?;
        let access_token = Text::new("paste your github access token for your private repos:").prompt()?;
        println!("Setting webhook for repos in your pipeline...");

    }

    Ok(())
}
// login to persys
async fn login() -> reqwest::Result<AuthUser> {
    // let cli = reqwest::Client::new();
    let body = reqwest::get("http://localhost:8551/auth/login").await?
        .json::<HashMap<String, String>>()
        .await?;
    let data = body.get("URL").unwrap().trim();
    // let url = ToString::to_string(data);
    //println!("{:#?}", data);
    // let _ib = github_login(url).await;
    let params = Url::parse(&data).unwrap().query_pairs().collect::<HashMap<_, _>>().get("state").unwrap().to_string();
    //let state = params.collect::<HashMap<_, _>>().get("state").unwrap().to_string();
    // let idemURL = "http://persys.eastus.cloudapp.azure.com/login/cli/" + state.get("state");

    // open browser
    open::that(data).unwrap();

    snailprint("waiting for you to complete login in the next 30 seconds!");
    let sec = time::Duration::from_secs(30);
    thread::sleep(sec);

    let mut map = HashMap::new();
    map.insert("state", &params);


    let url = "http://localhost:8551/auth/cli";

    let client = reqwest::Client::new();

    let res = client.post(url)
        .json(&map)
        .send()
        .await?
        .json::<AuthUser>().await?;

    // let data = reqwest::get(url).await?
    //     .json::<HashMap<String, String>>()
    //     .await?;

    return Ok(res)
}


async fn add_webhook(token: String, repo_id: f64) -> reqwest::Result<()> {
    println!("{}", repo_id);
    Ok(())
}

async fn add_access_token(token: String , access_token: String) -> reqwest::Result<()> {

    Ok(())
}

// async fn list_repos<'a>(token: String) -> Vec<&'a String> {
//
//     let client = reqwest::Client::new();
//
//     let mut repos = client.get("http://persys.eastus.cloudapp.azure.com/api/v1/repos/list")
//         .bearer_auth(token)
//         .send()
//         .await.unwrap()
//         .json::<HashMap<String, String>>()
//         .await.unwrap();
//
//     let v = Vec::from_iter(repos.get("name"));
//
//     return v
//
// }
