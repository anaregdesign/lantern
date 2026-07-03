"""Faithful reimplementation of the Liang et al. (WWW 2018) MSD preprocessing.

Reproduces the vae_cf notebook logic (dawenl/vae_cf) with the MSD settings
stated in the paper: min_uc=20, min_sc=200, n_heldout_users=50000, seed 98765.
Outputs compact integer arrays under out/.
"""

import os
import sys
import zipfile

import numpy as np
import pandas as pd

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "out")
os.makedirs(OUT, exist_ok=True)

MIN_UC = 20      # users with at least 20 songs
MIN_SC = 200     # songs listened to by at least 200 users
N_HELDOUT = 50000
SEED = 98765


def log(msg):
    print(msg, flush=True)


def get_count(tp, col):
    return tp.groupby(col).size()


def filter_triplets(tp, min_uc=MIN_UC, min_sc=MIN_SC):
    # Order matters and matches vae_cf: items first, then users.
    if min_sc > 0:
        itemcount = get_count(tp, "song")
        tp = tp[tp["song"].isin(itemcount.index[itemcount >= min_sc])]
    if min_uc > 0:
        usercount = get_count(tp, "user")
        tp = tp[tp["user"].isin(usercount.index[usercount >= min_uc])]
    usercount, itemcount = get_count(tp, "user"), get_count(tp, "song")
    return tp, usercount, itemcount


def split_train_test_proportion(data, test_prop=0.2):
    data_grouped_by_user = data.groupby("user")
    tr_list, te_list = [], []
    np.random.seed(SEED)
    for _, group in data_grouped_by_user:
        n_items_u = len(group)
        if n_items_u >= 5:
            idx = np.zeros(n_items_u, dtype="bool")
            idx[
                np.random.choice(
                    n_items_u, size=int(test_prop * n_items_u), replace=False
                ).astype("int64")
            ] = True
            tr_list.append(group[np.logical_not(idx)])
            te_list.append(group[idx])
        else:
            tr_list.append(group)
    return pd.concat(tr_list), pd.concat(te_list)


def main():
    zpath = os.path.join(HERE, "train_triplets.txt.zip")
    log(f"reading {zpath} ...")
    with zipfile.ZipFile(zpath) as z:
        name = z.namelist()[0]
        with z.open(name) as f:
            raw = pd.read_csv(
                f,
                sep="\t",
                header=None,
                names=["user", "song", "plays"],
                usecols=["user", "song"],
                dtype={"user": "string", "song": "string"},
            )
    log(f"raw triplets: {len(raw):,}")

    raw, usercount, itemcount = filter_triplets(raw)
    sparsity = 1.0 * len(raw) / (usercount.size * itemcount.size)
    log(
        f"after filtering: {len(raw):,} events, {usercount.size:,} users, "
        f"{itemcount.size:,} items, sparsity {sparsity*100:.3f}%"
    )

    unique_uid = usercount.index  # sorted by user id (groupby sorts keys)
    np.random.seed(SEED)
    idx_perm = np.random.permutation(unique_uid.size)
    unique_uid = unique_uid[idx_perm]

    n_users = unique_uid.size
    tr_users = unique_uid[: (n_users - N_HELDOUT * 2)]
    vd_users = unique_uid[(n_users - N_HELDOUT * 2) : (n_users - N_HELDOUT)]
    te_users = unique_uid[(n_users - N_HELDOUT) :]
    log(f"users: train {len(tr_users):,} / val {len(vd_users):,} / test {len(te_users):,}")

    train_plays = raw.loc[raw["user"].isin(tr_users)]
    unique_sid = pd.unique(train_plays["song"])
    log(f"items in training set: {len(unique_sid):,}")

    show2id = {sid: i for i, sid in enumerate(unique_sid)}
    profile2id = {pid: i for i, pid in enumerate(unique_uid)}

    def numerize(tp):
        uid = tp["user"].map(profile2id).to_numpy(dtype=np.int64)
        sid = tp["song"].map(show2id).to_numpy(dtype=np.int64)
        return uid, sid

    sid_set = set(unique_sid)

    vad_plays = raw.loc[raw["user"].isin(vd_users)]
    vad_plays = vad_plays.loc[vad_plays["song"].isin(sid_set)]
    vad_tr, vad_te = split_train_test_proportion(vad_plays)

    test_plays = raw.loc[raw["user"].isin(te_users)]
    test_plays = test_plays.loc[test_plays["song"].isin(sid_set)]
    test_tr, test_te = split_train_test_proportion(test_plays)

    tru, trs = numerize(train_plays)
    vtu, vts = numerize(vad_tr)
    veu, ves = numerize(vad_te)
    ttu, tts = numerize(test_tr)
    teu, tes = numerize(test_te)

    np.savez_compressed(
        os.path.join(OUT, "split.npz"),
        n_items=len(unique_sid),
        n_users=n_users,
        n_train_users=len(tr_users),
        train_uid=tru, train_sid=trs,
        vad_tr_uid=vtu, vad_tr_sid=vts,
        vad_te_uid=veu, vad_te_sid=ves,
        test_tr_uid=ttu, test_tr_sid=tts,
        test_te_uid=teu, test_te_sid=tes,
    )
    log("saved out/split.npz")
    log(
        f"train events {len(tru):,}; vad fold-in {len(vtu):,} / heldout {len(veu):,}; "
        f"test fold-in {len(ttu):,} / heldout {len(teu):,}"
    )


if __name__ == "__main__":
    sys.exit(main())
