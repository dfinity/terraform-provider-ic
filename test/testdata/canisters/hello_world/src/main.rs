#![no_main]

use ic_cdk::println;
use ic_cdk_macros::{init, post_upgrade, query};
use ic_stable_structures::{DefaultMemoryImpl, StableCell};
use std::cell::RefCell;

thread_local! {
    static GREETER: RefCell<StableCell<String, DefaultMemoryImpl>> =
           RefCell::new(StableCell::init(DefaultMemoryImpl::default(),"Hello".to_string()).unwrap());
}

#[init]
fn init(arg: Option<String>) {
    if let Some(greeter) = arg {
        GREETER.with_borrow_mut(|grt| grt.set(greeter)).unwrap();
    }
    let val = GREETER.with_borrow(|grt| grt.get().clone());

    println!("Init with greeter: {val}");
}

#[post_upgrade]
fn post_upgrade(arg: Option<String>) {
    init(arg)
}

#[query]
fn hello(arg: Option<String>) -> String {
    let greeter = GREETER.with_borrow(|grt| grt.get().clone());
    let greeted = arg.unwrap_or("World".to_string());

    format!("{greeter}, {greeted}!")
}
