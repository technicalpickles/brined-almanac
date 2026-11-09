use std::collections::{BTreeSet, HashMap};
use std::path::Path;

use almanac::prelude::*;
use serde::Deserialize;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Tier {
    Dormant,
    Deployed,
    Active,
    Maintained,
}

pub const WATCHED_BY_SECURITY_ALERTS: &[Tier] = &[Tier::Deployed, Tier::Active, Tier::Maintained];

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct Declaration {
    name: String,
    tiers: BTreeSet<Tier>,
    signal: Option<String>,
    note: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct File {
    #[serde(default)]
    repo: Vec<Declaration>,
}

#[derive(Debug)]
pub struct Tiers {
    // Slug has no Ord, so `watched` sorts by its string form.
    by_repo: HashMap<Slug, BTreeSet<Tier>>,
}

impl Tiers {
    pub fn parse(public: &str, local: Option<&str>) -> Result<Tiers, String> {
        let mut by_repo = HashMap::new();
        read("public tiers", public, &mut by_repo)?;
        if let Some(local) = local {
            read("local tiers", local, &mut by_repo)?;
        }
        Ok(Tiers { by_repo })
    }

    pub fn load(local: Option<&Path>) -> Result<Tiers, String> {
        let local = match local {
            Some(path) => Some(
                std::fs::read_to_string(path)
                    .map_err(|e| format!("reading {}: {e}", path.display()))?,
            ),
            None => {
                eprintln!("tiers: LOCAL_TIERS is not set, loading public tiers only");
                None
            }
        };
        Tiers::parse(include_str!("../tiers.toml"), local.as_deref())
    }

    pub fn watched(&self, any_of: &[Tier]) -> Vec<&Slug> {
        let mut repos: Vec<&Slug> = self
            .by_repo
            .iter()
            .filter(|(_, tiers)| any_of.iter().any(|t| tiers.contains(t)))
            .map(|(slug, _)| slug)
            .collect();
        repos.sort_by_key(|slug| slug.to_string());
        repos
    }
}

fn read(
    source: &str,
    text: &str,
    by_repo: &mut HashMap<Slug, BTreeSet<Tier>>,
) -> Result<(), String> {
    let file: File = toml::from_str(text).map_err(|e| format!("{source}: {e}"))?;
    for declaration in file.repo {
        let name = declaration.name;
        if name.matches('/').count() != 1 {
            return Err(format!("{source}: {name:?} is not owner/repo"));
        }
        let slug: Slug = name
            .parse()
            .map_err(|e| format!("{source}: {name:?}: {e}"))?;
        if declaration.tiers.is_empty() {
            return Err(format!("{source}: {name} declares no tiers"));
        }
        if by_repo.insert(slug, declaration.tiers).is_some() {
            return Err(format!(
                "{source}: {name} is declared twice, or in both the public and local files"
            ));
        }
    }
    Ok(())
}
