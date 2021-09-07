'use strict';
// general utility library
let utils = {
    tag: function(typ, str, clazz){
        var d = document.createElement(typ)
        if(str){
            d.innerText=""+str
        }
        if (clazz){
            d.classList.add(clazz)
        }
        return d
    },
    slantedHeading: function(title){
        // What we're aiming for:
        // <th class="rotate"><div><span>Column header 1</span></div></th>
        // See https://css-tricks.com/rotated-table-column-headers/
        let div = utils.tag("div")
        div.append(utils.tag("span", title))
        let th = utils.tag("th", )
        th.append(div)
        $(th).addClass("rotate")
        return th
    },

    // shortHash expects input to be a full 64-char hex input
    // and produces an abbreviated clickable represenation
    shortHash: function(hashstr){
        if (hashstr.length !== 66){
            return hashstr
        }
        hashstr =  hashstr.slice(2,8) // +"…"//+hashstr.slice(-6);
        return hashstr
    },
    etherscanLink : function(hash){
        let x = document.createElement("a");
        x.setAttribute("href","https://etherscan.io/block/"+hash);
        x.append("Etherscan")
        return x
    },
    link: function(target){
        let x = document.createElement("a");
        x.setAttribute("href", target);
        x.append(target)
        return x
    },
}

// little fifo to store hash->data mappings
let miniFIFO ={
    data: {},
    fifo: [],
    store: function(hash, data){
        if (this.data[hash]){
            return
        }
        this.data[hash] = data
        this.fifo.push(hash)
        if (this.fifo.length > 100){
            let first = this.fifo[0]
            this.fifo = this.fifo.slice(1)
            delete data[first]
        }
    },
    get : function(hash){
        return this.data[hash]
    },
    has: function(hash){
        return !!this.data[hash]
    }
}

// Little lib to provide human-friendly alternatives to the
// fields in a block header
let humanFriendly = {
    timestamp: function(val){
        let unix_time = parseInt(val, 16)
        return this.timeSince(new Date(unix_time*1000)) + " old"
    },
    gasUsed: (val) => parseInt(val, 16),
    gasLimit: (val) => parseInt(val, 16),
    number: (val) => parseInt(val, 16),
    difficulty: (val) => parseInt(val, 16),
    hash: utils.etherscanLink,
    parentHash: utils.etherscanLink,
    timeSince: function(date) {
        let seconds = Math.floor((new Date() - date) / 1000);
        let interval = seconds / 31536000;
        if (interval > 1) {
            return Math.floor(interval) + " years";
        }
        interval = seconds / 2592000;
        if (interval > 1) {
            return Math.floor(interval) + " months";
        }
        interval = seconds / 86400;
        if (interval > 1) {
            return Math.floor(interval) + " days";
        }
        interval = seconds / 3600;
        if (interval > 1) {
            return Math.floor(interval) + " hours";
        }
        interval = seconds / 60;
        if (interval > 1) {
            return Math.floor(interval) + " minutes";
        }
        return Math.floor(seconds) + " seconds";
    },
    extraData: function(hexdata) {
        let hex = hexdata.toString()
        let str = "";
        for (let i = 2; i < hex.length; i += 2){
            let ch = parseInt(hex.substr(i, 2), 16)
            if (ch >=32 && ch <= 126){
                str += String.fromCharCode(ch)
            }else{
                str += "."
            }
        }
        return str
    },
}


$(document).ready(function() {
    fetch()
});

function fetch(){
    // Retrieve the list of files
    $.ajax("data.json", {
        success: onData,
        failure: function(status, err){ alert(err); },
        cache: false,
    })
}

// for debugging
function progress(message){
    console.log(message)
    let  a = $("#debug").text();
    $("#debug").text((new Date()).toLocaleTimeString()+ " | " +message+"\n" +a );
}

// onData handles the main data chunks
function onData(data){
    // Set title
    if (data["Chain"]){
        $("title").text(data["Chain"])
    }
    // Populate node info
    var nodeB = $("#nodes tbody")
    nodeB.empty()

    // Clear headings
    var thead = $("#table thead")
    thead.empty()

    thead.append(utils.slantedHeading("Number"))

    data.Cols.forEach(function(client) {
        let name = client.Name
        let version = client.Version
        let status = "OK"
        let progress = "Never"
        let badblocks = "0"
        let vulnerabilites = client.Vulnerabilities || []
        if (client.LastProgress > 0){
            progress = humanFriendly.timeSince(new Date(client.LastProgress*1000)) + " ago"
        }
        if (client.Status != 0) {
            status = " (unhealthy)"
        }
        if (client.BadBlocks > 0) {
            badblocks = client.BadBlocks
        }
        let tRow = utils.tag("tr")
        tRow.append(utils.tag("td", name))
        tRow.append(utils.tag("td", version))
        tRow.append(utils.tag("td", status))
        tRow.append(utils.tag("td", progress))
        tRow.append(utils.tag("td", badblocks))
        let vulnTd = utils.tag("td");
        vulnerabilites.forEach(element => {
            let warn = utils.tag("span","", "glyphicon")
            warn.classList.add("glyphicon-warning-sign")
            vulnTd.append(warn)
            $(warn).on('click', function(){showVulnerability(element)})
        });
        tRow.append(vulnTd)
        
        nodeB.append(tRow)
        // Add td headings
        thead.append(utils.slantedHeading(name))
    })
    // Clear rows
    var tbody = $("#table tbody")
    tbody.empty()
    // Add rows
    data.Numbers.forEach(function(number) {
        number = ""+number
        var row = utils.tag("tr")
        row.append(utils.tag("td", number))
        var rowData = ""
        var count=0
        data.Rows[number].forEach(function(data){
            var hl = utils.shortHash(data)
            var td = utils.tag("td",hl)
            row.append(td)
            if (data.length == 0){ return }
            $(td).on('click', function(){showblock(data)})
            // Count how many even have this
            count = count+1
            if (rowData ==""){
                rowData = data
            }else if (rowData != data){
                rowData = "fail"
            }
        })
        if(count > 1){
            if (rowData=="fail"){
                $(row).addClass("danger")
            }else{
                $(row).addClass("success")
            }
        }
        tbody.append(row)
    })

    // Populate bad block info
    var badblocksB = $("#badblocks tbody")
    badblocksB.empty()
    data.BadBlocks.forEach(function(badblock) {
        let tRow = utils.tag("tr")
        tRow.append(utils.tag("td", badblock.number))
        tRow.append(utils.tag("td", utils.shortHash(badblock.hash)))
        tRow.append(utils.tag("td", badblock.clients))
        $(tRow).on('click', function(){
            showBadBlock( badblock.hash)
        })
        badblocksB.append(tRow)
    })
}

function showBadBlock( hash){
    $.ajax("badblocks/"+hash+".json", {
        dataType: "json",
        success: function(data){
            populateBlockInfo(data)
        },
        error: function(status, err){
            populateBlockInfo({"hash": hash})
            progress("Failed to fetch bad block: " + status.statusText + " error: " + err);
            },
    })
}

function showVulnerability(vuln) {
    $.ajax("vulns/"+vuln+".json", {
        dataType: "json",
        success: function(data){

            $(".modal-title").text(data.Severity +" : " + data.Name + " ( " + data.Uid+" )")
            let tbody = $(".modal-body")
            tbody.empty()
            for (let [key, value] of Object.entries(data)) {
                if (key == 'Name' || key == 'Check' || key == "Uid") {
                    continue
                }
                if (key == 'Links') {
                    let ul = utils.tag("ul")
                    value.forEach(function(target){
                        let li = utils.tag("li")
                        li.append(utils.link(target))
                        ul.append(li)

                    })
                    tbody.append(ul)
                }else if (key == "Summary" || key == "Description"){
                    tbody.append(utils.tag("strong", key))
                    tbody.append(utils.tag("p", value))
                }else{
                    let p = utils.tag("p")
                    p.append(utils.tag("strong", key+" : "))
                    p.append(utils.tag("span", value))
                    tbody.append(p)
                }
            }
            $("#myModal").modal()
        },
        error: function(status, err){
            let msg = "Failed to fetch vulnerability: " + status.statusText + " error: " + err
            $(".modal-body").text(msg);
            $("#myModal").modal()
            progress(msg);
        },
    })
}

// populateBlockInfo redraws the Block Info section with the given data
function populateBlockInfo(data){
    let tbody = $("#block tbody")
    tbody.empty()
    for (let [key, value] of Object.entries(data)) {
        let row = utils.tag("tr")
        row.append(utils.tag("td", key))
        let v = utils.tag("td")
        v.append(utils.tag("code", value))
        if (humanFriendly[key]){
            v.append(humanFriendly[key](value))
        }
        row.append(v)
        tbody.append(row)
    }
}

// Calling this method means that info about the given hash should be displayed.
// If we have it locally, wham-bam-done, otherwise we fetch it from the server first.
function showblock(hash){
    // Using a fifo here isn't only to avoid network traffic, it's also
    // to minimize flickering when clearing the table
    let data = miniFIFO.get(hash)
    if (data){
        populateBlockInfo(data)
    }else{
        $.ajax("hashes/"+hash+".json", {
            dataType: "json",
            success: function(data){
                miniFIFO.store(hash, data)
                populateBlockInfo(data)
            },
            error: function(status, err){
                populateBlockInfo({"hash": hash})
                progress("Failed to fetch hash: " + status.statusText + " error: " + err);
                },
        })
    }
}
