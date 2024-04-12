#![no_main]

use ic_cdk::api::call::arg_data_raw;
use ic_cdk::println;
use ic_cdk_macros::{post_upgrade, query};
use ic_stable_structures::{DefaultMemoryImpl, StableCell};
use std::cell::RefCell;

thread_local! {
    static GREETER: RefCell<StableCell<String, DefaultMemoryImpl>> =
           RefCell::new(StableCell::init(DefaultMemoryImpl::default(),"Hello".to_string()).unwrap());
}

#[export_name = "canister_init"]
fn init() {
    /* Here we do the whole arg parsing manually to allow passing an empty bytestring */
    ic_cdk::setup();

    let arg_raw = arg_data_raw();
    let default_greeter = "Hello".to_string();

    let greeter = if arg_raw.is_empty() {
        default_greeter
    } else {
        let (arg,): (Option<String>,) =
            candid::decode_args(&arg_raw).expect("Could not decode init args");
        arg.unwrap_or(default_greeter)
    };
    GREETER.with_borrow_mut(|grt| grt.set(greeter)).unwrap();

    let val = GREETER.with_borrow(|grt| grt.get().clone());

    println!("Init with greeter: {val}");
}

#[post_upgrade]
fn post_upgrade() {
    init()
}

#[query]
fn hello(arg: Option<String>) -> String {
    let greeter = GREETER.with_borrow(|grt| grt.get().clone());
    let greeted = arg.unwrap_or("World".to_string());

    format!("{greeter}, {greeted}!")
}
