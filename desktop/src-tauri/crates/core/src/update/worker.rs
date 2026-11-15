// SPDX-License-Identifier: AGPL-3.0-only

//! The thread of the network of an update (specs/2026-11-13-desk-updates.md UB1.3, UB2.2): it checks the addresses and
//! downloads a file one thing after another, so the supervision never waits for the network; what it found goes back to
//! the driver as [`Done`]. A cancel stops a download between two reads.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, Sender};
use std::sync::Arc;
use std::thread;

use super::fetch::{Checked, Downloaded, Network, NotDownloaded};
use super::manifest::Release;
use super::Kind;

/// What the thread found.
#[derive(Debug, Clone, PartialEq)]
pub enum Done {
    /// The walk over the addresses ended.
    Checked(Checked),
    /// The download of `version` is at `percent`.
    Progress { version: String, percent: u8 },
    /// The download of a version ended: the file checked, or why not.
    Downloaded(Result<Downloaded, (String, NotDownloaded)>),
}

enum Job {
    Check,
    Download(Box<Release>),
}

/// The thread and its cancel.
pub struct Worker {
    jobs: Sender<Job>,
    cancel: Arc<AtomicBool>,
}

impl Worker {
    /// Starts the thread over `network` for the kind of this installation; `told` hears what it found.
    pub fn start(network: Network, kind: Kind, told: impl Fn(Done) + Send + 'static) -> Worker {
        let (jobs, asked) = mpsc::channel::<Job>();
        let cancel = Arc::new(AtomicBool::new(false));
        let canceled = Arc::clone(&cancel);
        thread::spawn(move || {
            for job in asked {
                match job {
                    Job::Check => told(Done::Checked(network.check(kind))),
                    Job::Download(release) => {
                        let release = *release;
                        let version = release.version.to_string();
                        let result = network.download(&release, &canceled, |percent| {
                            told(Done::Progress { version: version.clone(), percent });
                        });
                        told(Done::Downloaded(result.map_err(|reason| (version, reason))));
                    }
                }
            }
        });
        Worker { jobs, cancel }
    }

    /// Checks the addresses.
    pub fn check(&self) {
        let _ = self.jobs.send(Job::Check);
    }

    /// Downloads the file of `release`; a cancel asked before stops nothing of it.
    pub fn download(&self, release: Release) {
        self.cancel.store(false, Ordering::SeqCst);
        let _ = self.jobs.send(Job::Download(Box::new(release)));
    }

    /// Stops the download that goes on.
    pub fn cancel(&self) {
        self.cancel.store(true, Ordering::SeqCst);
    }
}
