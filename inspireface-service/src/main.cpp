#include <inspireface.h>
#include <herror.h>

#include <httplib.h>
#include <nlohmann/json.hpp>

#include <opencv2/core.hpp>
#include <opencv2/imgcodecs.hpp>
#include <opencv2/imgproc.hpp>

#include <atomic>
#include <cctype>
#include <csignal>
#include <cstdlib>
#include <cstring>
#include <iostream>
#include <limits>
#include <mutex>
#include <optional>
#include <stdexcept>
#include <string>
#include <vector>

using json = nlohmann::json;

static std::atomic<httplib::Server*> g_server{nullptr};

static void signal_handler(int) {
  auto* server = g_server.load();
  if (server != nullptr) {
    server->stop();
  }
}

struct Config {
  std::string host = "0.0.0.0";
  int port = 18080;
  std::string pack_path;
  int max_faces = 5;
  int detect_pixel_level = 320;
  int min_face_px = 32;
  float detect_threshold = -1.0f;
  bool enable_quality = true;
  bool enable_pose = true;
  size_t max_upload_bytes = 10 * 1024 * 1024;
};

static std::string getenv_or(const char* name, const std::string& fallback) {
  const char* v = std::getenv(name);
  return v && *v ? std::string(v) : fallback;
}

static int getenv_int_or(const char* name, int fallback) {
  const char* v = std::getenv(name);
  if (!v || !*v) return fallback;
  try {
    return std::stoi(v);
  } catch (...) {
    return fallback;
  }
}

static float getenv_float_or(const char* name, float fallback) {
  const char* v = std::getenv(name);
  if (!v || !*v) return fallback;
  try {
    return std::stof(v);
  } catch (...) {
    return fallback;
  }
}

static bool getenv_bool_or(const char* name, bool fallback) {
  const char* v = std::getenv(name);
  if (!v || !*v) return fallback;

  std::string s(v);
  for (auto& c : s) {
    c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
  }

  return s == "1" || s == "true" || s == "yes" || s == "on";
}

static Config load_config(int argc, char** argv) {
  Config cfg;

  cfg.host = getenv_or("FACE_SERVICE_HOST", cfg.host);
  cfg.port = getenv_int_or("FACE_SERVICE_PORT", cfg.port);
  cfg.pack_path = getenv_or("INSPIREFACE_PACK_PATH", cfg.pack_path);
  cfg.max_faces = getenv_int_or("FACE_MAX_FACES", cfg.max_faces);
  cfg.detect_pixel_level = getenv_int_or("FACE_DETECT_PIXEL_LEVEL", cfg.detect_pixel_level);
  cfg.min_face_px = getenv_int_or("FACE_MIN_FACE_PX", cfg.min_face_px);
  cfg.detect_threshold = getenv_float_or("FACE_DETECT_THRESHOLD", cfg.detect_threshold);
  cfg.enable_quality = getenv_bool_or("FACE_ENABLE_QUALITY", cfg.enable_quality);
  cfg.enable_pose = getenv_bool_or("FACE_ENABLE_POSE", cfg.enable_pose);
  cfg.max_upload_bytes = static_cast<size_t>(getenv_int_or("FACE_MAX_UPLOAD_MB", 10)) * 1024 * 1024;

  for (int i = 1; i < argc; ++i) {
    std::string arg(argv[i]);

    auto next = [&]() -> std::string {
      if (i + 1 >= argc) {
        throw std::runtime_error("missing value for " + arg);
      }
      return argv[++i];
    };

    if (arg == "--host") {
      cfg.host = next();
    } else if (arg == "--port") {
      cfg.port = std::stoi(next());
    } else if (arg == "--pack") {
      cfg.pack_path = next();
    } else if (arg == "--max-faces") {
      cfg.max_faces = std::stoi(next());
    } else if (arg == "--detect-pixel-level") {
      cfg.detect_pixel_level = std::stoi(next());
    } else if (arg == "--min-face-px") {
      cfg.min_face_px = std::stoi(next());
    } else if (arg == "--detect-threshold") {
      cfg.detect_threshold = std::stof(next());
    } else if (arg == "--no-quality") {
      cfg.enable_quality = false;
    } else if (arg == "--no-pose") {
      cfg.enable_pose = false;
    } else if (arg == "--max-upload-mb") {
      cfg.max_upload_bytes = static_cast<size_t>(std::stoi(next())) * 1024 * 1024;
    } else if (arg == "--help" || arg == "-h") {
      std::cout
          << "Usage: face-service --pack /path/to/Megatron [options]\n"
          << "Options:\n"
          << "  --host 0.0.0.0\n"
          << "  --port 18080\n"
          << "  --max-faces 5\n"
          << "  --detect-pixel-level 320\n"
          << "  --min-face-px 32\n"
          << "  --detect-threshold 0.5\n"
          << "  --no-quality\n"
          << "  --no-pose\n"
          << "  --max-upload-mb 10\n";
      std::exit(0);
    } else {
      throw std::runtime_error("unknown argument: " + arg);
    }
  }

  if (cfg.pack_path.empty()) {
    throw std::runtime_error("missing pack path: pass --pack or set INSPIREFACE_PACK_PATH");
  }

  return cfg;
}

static json error_json(const std::string& code, const std::string& message, long inspireface_code = 0) {
  json j;
  j["ok"] = false;
  j["error"] = {
      {"code", code},
      {"message", message},
  };

  if (inspireface_code != 0) {
    j["error"]["inspireface_code"] = inspireface_code;
  }

  return j;
}

static void send_json(httplib::Response& res, int status, const json& body) {
  res.status = status;
  res.set_content(body.dump(), "application/json; charset=utf-8");
}

static cv::Mat decode_image_bytes_to_bgr_mat(const std::string& bytes) {
  if (bytes.empty()) {
    throw std::runtime_error("empty image bytes");
  }

  if (bytes.size() > static_cast<size_t>(std::numeric_limits<int>::max())) {
    throw std::runtime_error("image bytes too large for OpenCV decoder");
  }

  std::vector<unsigned char> buffer(bytes.begin(), bytes.end());

  cv::Mat frame = cv::imdecode(buffer, cv::IMREAD_COLOR);
  if (frame.empty()) {
    throw std::runtime_error("OpenCV imdecode failed: unsupported or corrupted image");
  }

  if (frame.type() != CV_8UC3) {
    if (frame.channels() == 1) {
      cv::cvtColor(frame, frame, cv::COLOR_GRAY2BGR);
    } else if (frame.channels() == 4) {
      cv::cvtColor(frame, frame, cv::COLOR_BGRA2BGR);
    } else {
      throw std::runtime_error("decoded image is not CV_8UC3");
    }
  }

  if (!frame.isContinuous()) {
    frame = frame.clone();
  }

  return frame;
}

class InspireFaceEngine {
 public:
  explicit InspireFaceEngine(const Config& cfg) : cfg_(cfg) {
    HResult ret = HFLaunchInspireFace(cfg_.pack_path.c_str());
    if (ret != HSUCCEED) {
      throw std::runtime_error("HFLaunchInspireFace failed: " + std::to_string(ret));
    }

    HOption option = HF_ENABLE_FACE_RECOGNITION;
    if (cfg_.enable_quality) {
      option |= HF_ENABLE_QUALITY;
    }
    if (cfg_.enable_pose) {
      option |= HF_ENABLE_FACE_POSE;
    }

    ret = HFCreateInspireFaceSessionOptional(
        option,
        HF_DETECT_MODE_ALWAYS_DETECT,
        cfg_.max_faces,
        cfg_.detect_pixel_level,
        -1,
        &session_);

    if (ret != HSUCCEED) {
      HFTerminateInspireFace();
      throw std::runtime_error("HFCreateInspireFaceSessionOptional failed: " + std::to_string(ret));
    }

    HFSessionSetTrackPreviewSize(session_, cfg_.detect_pixel_level);
    HFSessionSetFilterMinimumFacePixelSize(session_, cfg_.min_face_px);

    if (cfg_.detect_threshold >= 0.0f) {
      HFSessionSetFaceDetectThreshold(session_, cfg_.detect_threshold);
    }

    int len = 0;
    ret = HFGetFeatureLength(&len);
    if (ret == HSUCCEED && len > 0) {
      feature_len_ = len;
    }

    float threshold = 0.0f;
    ret = HFGetRecommendedCosineThreshold(&threshold);
    if (ret == HSUCCEED) {
      recommended_threshold_ = threshold;
    }
  }

  ~InspireFaceEngine() {
    if (session_ != nullptr) {
      HFReleaseInspireFaceSession(session_);
      session_ = nullptr;
    }

    HFTerminateInspireFace();
  }

  json health() const {
    json j;
    j["ok"] = true;
    j["service"] = "inspireface-service";
    j["image_decoder"] = "opencv-imdecode";
    j["feature_length"] = feature_len_;

    if (recommended_threshold_) {
      j["recommended_cosine_threshold"] = *recommended_threshold_;
    }

    j["config"] = {
        {"max_faces", cfg_.max_faces},
        {"detect_pixel_level", cfg_.detect_pixel_level},
        {"min_face_px", cfg_.min_face_px},
        {"quality", cfg_.enable_quality},
        {"pose", cfg_.enable_pose},
    };

    return j;
  }

  json extract_from_bgr_mat(const cv::Mat& frame, bool best_only) {
    std::lock_guard<std::mutex> lock(mu_);

    if (frame.empty()) {
      return error_json("EMPTY_FRAME", "decoded frame is empty");
    }

    if (frame.type() != CV_8UC3) {
      return error_json("BAD_FRAME_FORMAT", "frame must be CV_8UC3 BGR");
    }

    if (!frame.isContinuous()) {
      return error_json("BAD_FRAME_MEMORY", "frame memory must be continuous");
    }

    HFImageBitmap image = nullptr;
    HFImageStream stream = nullptr;
    HFMultipleFaceData faces = {0};

    HFImageBitmapData bitmap_data = {0};
    bitmap_data.data = const_cast<unsigned char*>(frame.data);
    bitmap_data.width = frame.cols;
    bitmap_data.height = frame.rows;
    bitmap_data.channels = frame.channels();

    HResult ret = HFCreateImageBitmap(&bitmap_data, &image);
    if (ret != HSUCCEED) {
      return error_json("IMAGE_BITMAP_FAILED", "HFCreateImageBitmap failed", ret);
    }

    ret = HFCreateImageStreamFromImageBitmap(image, HF_CAMERA_ROTATION_0, &stream);
    if (ret != HSUCCEED) {
      HFReleaseImageBitmap(image);
      return error_json("IMAGE_STREAM_FAILED", "HFCreateImageStreamFromImageBitmap failed", ret);
    }

    ret = HFExecuteFaceTrack(session_, stream, &faces);
    if (ret != HSUCCEED) {
      HFReleaseImageStream(stream);
      HFReleaseImageBitmap(image);
      return error_json("FACE_DETECT_FAILED", "HFExecuteFaceTrack failed", ret);
    }

    json out;
    out["ok"] = true;
    out["face_count"] = faces.detectedNum;
    out["decoder"] = "opencv";
    out["image"] = {
        {"width", frame.cols},
        {"height", frame.rows},
        {"channels", frame.channels()},
    };

    if (recommended_threshold_) {
      out["recommended_cosine_threshold"] = *recommended_threshold_;
    }

    out["faces"] = json::array();

    int start = 0;
    int end = faces.detectedNum;

    if (best_only && faces.detectedNum > 0) {
      start = best_face_index(faces);
      end = start + 1;
    }

    for (int i = start; i < end; ++i) {
      json face;
      face["index"] = i;

      if (faces.rects != nullptr) {
        const auto& r = faces.rects[i];
        face["box"] = {r.x, r.y, r.width, r.height};
      }

      if (faces.detConfidence != nullptr) {
        face["det_score"] = faces.detConfidence[i];
      }

      if (faces.trackIds != nullptr) {
        face["track_id"] = faces.trackIds[i];
      }

      if (cfg_.enable_pose && faces.angles.roll && faces.angles.yaw && faces.angles.pitch) {
        face["pose"] = {
            {"roll", faces.angles.roll[i]},
            {"yaw", faces.angles.yaw[i]},
            {"pitch", faces.angles.pitch[i]},
        };
      }

      if (cfg_.enable_quality) {
        float quality = 0.0f;
        HResult qret = HFFaceQualityDetect(session_, faces.tokens[i], &quality);
        if (qret == HSUCCEED) {
          face["quality"] = quality;
        } else {
          face["quality_error"] = qret;
        }
      }

      HFFaceFeature feature = {0};
      ret = HFCreateFaceFeature(&feature);
      if (ret != HSUCCEED) {
        face["embedding_error"] = ret;
        out["faces"].push_back(std::move(face));
        continue;
      }

      ret = HFFaceFeatureExtractTo(session_, stream, faces.tokens[i], feature);
      if (ret == HSUCCEED && feature.data != nullptr) {
        int len = feature_len_ > 0 ? feature_len_ : feature.size;

        json emb = json::array();
        for (int k = 0; k < len; ++k) {
          emb.push_back(feature.data[k]);
        }

        face["embedding"] = std::move(emb);
        face["embedding_dim"] = len;
      } else {
        face["embedding_error"] = ret;
      }

      HFReleaseFaceFeature(&feature);
      out["faces"].push_back(std::move(face));
    }

    HFReleaseImageStream(stream);
    HFReleaseImageBitmap(image);

    return out;
  }

 private:
  int best_face_index(const HFMultipleFaceData& faces) const {
    int best = 0;
    float best_score = -1.0f;

    for (int i = 0; i < faces.detectedNum; ++i) {
      float score = 0.0f;

      if (faces.detConfidence != nullptr) {
        score += faces.detConfidence[i];
      }

      if (faces.rects != nullptr) {
        score += 0.000001f * faces.rects[i].width * faces.rects[i].height;
      }

      if (score > best_score) {
        best_score = score;
        best = i;
      }
    }

    return best;
  }

  Config cfg_;
  HFSession session_ = nullptr;
  int feature_len_ = 0;
  std::optional<float> recommended_threshold_;
  std::mutex mu_;
};

static std::optional<std::string> request_image_bytes(const httplib::Request& req) {
  if (req.has_file("image")) {
    return req.get_file_value("image").content;
  }

  if (req.has_file("file")) {
    return req.get_file_value("file").content;
  }

  if (!req.body.empty()) {
    return req.body;
  }

  return std::nullopt;
}

int main(int argc, char** argv) {
  std::signal(SIGINT, signal_handler);
  std::signal(SIGTERM, signal_handler);

  Config cfg;

  try {
    cfg = load_config(argc, argv);
  } catch (const std::exception& e) {
    std::cerr << "config error: " << e.what() << "\n";
    return 2;
  }

  std::unique_ptr<InspireFaceEngine> engine;

  try {
    engine = std::make_unique<InspireFaceEngine>(cfg);
  } catch (const std::exception& e) {
    std::cerr << "engine init error: " << e.what() << "\n";
    return 3;
  }

  httplib::Server svr;

  svr.set_read_timeout(30, 0);
  svr.set_write_timeout(30, 0);
  svr.set_payload_max_length(cfg.max_upload_bytes);

  svr.Get("/health", [&](const httplib::Request&, httplib::Response& res) {
    send_json(res, 200, engine->health());
  });

  auto extract_handler = [&](const httplib::Request& req, httplib::Response& res, bool best_only) {
    try {
      auto bytes = request_image_bytes(req);
      if (!bytes || bytes->empty()) {
        send_json(res, 400, error_json("NO_IMAGE", "send multipart field 'image'/'file' or raw image bytes"));
        return;
      }

      if (bytes->size() > cfg.max_upload_bytes) {
        send_json(res, 413, error_json("IMAGE_TOO_LARGE", "image exceeds max upload size"));
        return;
      }

      cv::Mat frame = decode_image_bytes_to_bgr_mat(*bytes);
      json result = engine->extract_from_bgr_mat(frame, best_only);

      if (!result.value("ok", false)) {
        send_json(res, 422, result);
        return;
      }

      send_json(res, 200, result);
    } catch (const std::exception& e) {
      send_json(res, 500, error_json("INTERNAL_ERROR", e.what()));
    }
  };

  svr.Post("/extract", [&](const httplib::Request& req, httplib::Response& res) {
    bool best_only = req.has_param("best") && req.get_param_value("best") == "1";
    extract_handler(req, res, best_only);
  });

  svr.Post("/extract-best", [&](const httplib::Request& req, httplib::Response& res) {
    extract_handler(req, res, true);
  });

  std::cout << "face-service listening on http://" << cfg.host << ":" << cfg.port << "\n";
  std::cout << "pack path: " << cfg.pack_path << "\n";
  std::cout << "image decoder: OpenCV imdecode, no temp file\n";

  g_server.store(&svr);
  bool listened = svr.listen(cfg.host, cfg.port);
  g_server.store(nullptr);

  if (!listened) {
    std::cerr << "listen failed on " << cfg.host << ":" << cfg.port << "\n";
    return 4;
  }

  std::cout << "face-service stopped\n";
  return 0;
}