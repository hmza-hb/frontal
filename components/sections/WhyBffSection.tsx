"use client";

import { motion } from "framer-motion";
import { ShieldCheck, Award, Users, Cpu, CheckCircle2 } from "lucide-react";

export default function WhyBffSection() {
  const pillars = [
    {
      num: "01",
      icon: Award,
      title: "Industry Expertise",
      description:
        "Deep understanding of how great entertainment projects are developed and brought to audiences.",
      tag: "STUDIO LEADERSHIP",
    },
    {
      num: "02",
      icon: ShieldCheck,
      title: "Curated Opportunities",
      description:
        "A focus on meaningful projects with strong creative and commercial potential.",
      tag: "VETTING STANDARDS",
    },
    {
      num: "03",
      icon: Users,
      title: "Community Driven",
      description:
        "Connecting creators and audiences through shared participation.",
      tag: "GLOBAL BACKERS",
    },
    {
      num: "04",
      icon: Cpu,
      title: "Future Focused",
      description:
        "Building the infrastructure for the next era of entertainment financing.",
      tag: "NEXT-GEN PLATFORM",
    },
  ];

  return (
    <section id="why-bff" className="py-24 md:py-32 bg-white border-b border-slate-200/60 relative">
      <div className="max-w-6xl mx-auto px-4 sm:px-6">
        
        {/* Section Header */}
        <div className="max-w-3xl space-y-4 mb-16">
          <div className="inline-flex items-center gap-2 px-3 py-1 rounded-md bg-emerald-100 text-emerald-800 font-mono text-xs uppercase tracking-wider">
            <ShieldCheck className="w-3.5 h-3.5" />
            <span>— SECTION 5: WHY BFF</span>
          </div>

          <h2 className="text-3xl sm:text-4xl md:text-5xl font-extrabold tracking-tight text-slate-900 leading-tight">
            Built From Inside the Entertainment Industry.
          </h2>

          <p className="text-lg text-slate-600 font-normal leading-relaxed">
            Big Film Fund combines entertainment expertise, technology, and community-driven financing to create a more transparent and accessible future for film investment.
          </p>
        </div>

        {/* Four Trust Pillars Grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
          {pillars.map((pillar, idx) => {
            const Icon = pillar.icon;
            return (
              <motion.div
                key={pillar.title}
                initial={{ opacity: 0, y: 20 }}
                whileInView={{ opacity: 1, y: 0 }}
                viewport={{ once: true }}
                transition={{ duration: 0.5, delay: idx * 0.1 }}
                className="p-8 rounded-2xl bg-slate-50 border border-slate-200 hover:border-slate-300 hover:shadow-xl transition-all duration-300 group flex flex-col justify-between"
              >
                <div>
                  <div className="flex items-center justify-between mb-6">
                    <span className="font-mono text-xl font-bold text-slate-400 group-hover:text-slate-900 transition-colors">
                      PILLAR {pillar.num}
                    </span>
                    <span className="text-[10px] font-mono px-2.5 py-1 rounded bg-white border border-slate-200 text-slate-700 tracking-wider">
                      {pillar.tag}
                    </span>
                  </div>

                  <div className="w-12 h-12 rounded-xl bg-white border border-slate-200 flex items-center justify-center text-slate-900 mb-6 group-hover:bg-slate-900 group-hover:text-white transition-colors duration-300 shadow-sm">
                    <Icon className="w-6 h-6" />
                  </div>

                  <h3 className="text-2xl font-bold text-slate-900 mb-3">
                    {pillar.title}
                  </h3>

                  <p className="text-base text-slate-600 leading-relaxed">
                    {pillar.description}
                  </p>
                </div>

                <div className="mt-8 pt-4 border-t border-slate-200/60 flex items-center gap-2 text-xs font-mono text-emerald-700 font-semibold">
                  <CheckCircle2 className="w-4 h-4 text-emerald-600" />
                  <span>INSTITUTIONAL TRUST GUARANTEE</span>
                </div>
              </motion.div>
            );
          })}
        </div>

      </div>
    </section>
  );
}
